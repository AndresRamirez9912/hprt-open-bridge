package hostnet

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/AndresRamirez9912/photo-print-bridge/internal/core/domains/network"
)

// TestParsePing checks the summary of ping for good, partial and lost series
func TestParsePing(t *testing.T) {
	// Real ping summaries and an unreadable one
	testCases := []struct {
		name    string
		out     string
		loss    float64
		avg     time.Duration
		noTimes bool
	}{
		{name: "all replies", out: "20 packets transmitted, 20 received, 0% packet loss, time 4756ms\nrtt min/avg/max/mdev = 8.1/99.6/351.2/80.1 ms", loss: 0, avg: 100 * time.Millisecond},
		{name: "partial loss", out: "20 packets transmitted, 15 received, 25% packet loss\nrtt min/avg/max/mdev = 2.0/4.4/9.0/1.0 ms", loss: 25, avg: 4 * time.Millisecond},
		{name: "nothing came back", out: "20 packets transmitted, 0 received, 100% packet loss", loss: 100, noTimes: true},
		{name: "unreadable output", out: "garbage", loss: 100, noTimes: true},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			// Loss is always reported
			r := parsePing(tc.out, 20)
			if r.LossPct != tc.loss {
				t.Errorf("loss = %v, want %v", r.LossPct, tc.loss)
			}

			// Round trips only when some reply arrived
			if tc.noTimes {
				if r.RTTAvg != nil {
					t.Errorf("avg = %v, want nil", *r.RTTAvg)
				}
				return
			}
			if r.RTTAvg == nil || *r.RTTAvg != tc.avg {
				t.Errorf("avg = %v, want %v", r.RTTAvg, tc.avg)
			}
		})
	}
}

// TestParseHostWifi checks the reading of the network in use
func TestParseHostWifi(t *testing.T) {
	// Terse nmcli lines: IN-USE SSID SIGNAL FREQ CHAN
	testCases := []struct {
		name string
		out  string
		want *network.HostWifi
	}{
		{name: "network in use", out: " :Other:40:5180 MHz:36\n*:FAMILIA RC 2.4:72:2437 MHz:6\n", want: &network.HostWifi{SSID: "FAMILIA RC 2.4", Signal: 72, Freq: "2437 MHz", Channel: 6}},
		{name: "ssid with colons", out: "*:a:b:c:50:2412 MHz:1", want: &network.HostWifi{SSID: "a:b:c", Signal: 50, Freq: "2412 MHz", Channel: 1}},
		{name: "not associated", out: " :Other:40:5180 MHz:36", want: nil},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			got := parseHostWifi(tc.out)
			if (got == nil) != (tc.want == nil) || (got != nil && *got != *tc.want) {
				t.Errorf("got %+v, want %+v", got, tc.want)
			}
		})
	}
}

// TestParseWifiScan checks deduplication, the 2.4 GHz flag and the order of the scan
func TestParseWifiScan(t *testing.T) {
	// One scan with a dual band network, an escaped colon and a hidden network
	out := strings.Join([]string{
		"Home:80:5180 MHz:WPA2",
		"Home:60:2437 MHz:WPA2",
		"Guest:90:5200 MHz:WPA2",
		`Cafe\:2G:40:2412 MHz:`,
		":30:2412 MHz:WPA2",
	}, "\n")
	got := parseWifiScan(out)
	if len(got) != 3 {
		t.Fatalf("networks = %+v, want 3 without the hidden one", got)
	}

	// The expected network at each position of the result
	testCases := []struct {
		name   string
		index  int
		ssid   string
		band24 bool
		signal int
	}{
		{name: "the 2.4 GHz access point of a name wins", index: 0, ssid: "Home", band24: true, signal: 60},
		{name: "escaped colon in the name", index: 1, ssid: "Cafe:2G", band24: true, signal: 40},
		{name: "5 GHz networks go last", index: 2, ssid: "Guest", band24: false, signal: 90},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			n := got[tc.index]
			if n.SSID != tc.ssid || n.Band24 != tc.band24 || n.Signal != tc.signal {
				t.Errorf("network %d = %+v", tc.index, n)
			}
		})
	}
}

// fakeRunner answers each command with the output registered for its name and first arguments
type fakeRunner map[string]string

// run implements Runner, a missing command fails like a command that is not installed
func (f fakeRunner) run(_ context.Context, _ time.Duration, name string, args ...string) (string, error) {
	// The key is the command line, matched by prefix
	key := name
	if len(args) > 0 {
		key += " " + strings.Join(args, " ")
	}

	// Answer the registered output, or fail
	for prefix, out := range f {
		if strings.HasPrefix(key, prefix) {
			return out, nil
		}
	}
	return "", errors.New("not found")
}

// TestInspector checks the gateway fallback and the scan errors with fake commands
func TestInspector(t *testing.T) {
	// Each case is the set of commands the machine answers
	testCases := []struct {
		name        string
		commands    fakeRunner
		wantGateway string
		wantSource  string
		wantGwErr   error
		wantScanErr error
	}{
		{
			name:        "default route",
			commands:    fakeRunner{"ip -4 route": "default via 192.168.2.1 dev wlp2s0 proto dhcp", "nmcli -t -e yes": "Home:60:2437 MHz:WPA2"},
			wantGateway: "192.168.2.1", wantSource: "default route",
		},
		{
			name: "hotspot without router option",
			commands: fakeRunner{
				"ip -4 route":           "",
				"nmcli -t -f DEVICE":    "lo:loopback\nwlp2s0:wifi",
				"nmcli -g DHCP4.OPTION": "dhcp_server_identifier = 192.168.4.1 | expiry = 1",
				"nmcli -t -e yes":       "",
				"nmcli -t -e no":        "",
			},
			wantGateway: "192.168.4.1", wantSource: "dhcp server",
		},
		{name: "no network and no nmcli", commands: fakeRunner{}, wantGwErr: network.ErrNoWifiNetwork, wantScanErr: network.ErrScanUnavailable},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			in := NewInspector(tc.commands.run)

			// Gateway, with the hotspot fallback
			gw, err := in.Gateway(context.Background())
			if !errors.Is(err, tc.wantGwErr) {
				t.Fatalf("Gateway err = %v, want %v", err, tc.wantGwErr)
			}
			if err == nil && (gw.Address.String() != tc.wantGateway || gw.Source != tc.wantSource) {
				t.Errorf("gateway = %+v", gw)
			}

			// Scan, unavailable without nmcli
			if _, err := in.ScanWifi(context.Background()); !errors.Is(err, tc.wantScanErr) {
				t.Errorf("ScanWifi err = %v, want %v", err, tc.wantScanErr)
			}
		})
	}
}
