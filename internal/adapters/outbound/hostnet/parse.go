package hostnet

import (
	"math"
	"net/netip"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/AndresRamirez9912/photo-print-bridge/internal/core/domains/network"
)

// Patterns of the ping, ip and nmcli outputs
var (
	lossPattern   = regexp.MustCompile(`([\d.]+)% packet loss`)
	rttPattern    = regexp.MustCompile(`= ([\d.]+)/([\d.]+)/([\d.]+)`)
	routePattern  = regexp.MustCompile(`default via (\d+\.\d+\.\d+\.\d+) dev (\S+)`)
	dhcpPattern   = regexp.MustCompile(`dhcp_server_identifier = (\d+\.\d+\.\d+\.\d+)`)
	escapedColon  = regexp.MustCompile(`\\:`)
	nmcliSentinel = "\x00"
)

// parsePing reads the summary lines of ping, a missing loss line means every request was lost
func parsePing(out string, sent int) *network.PingReport {
	// Start from a total loss and fill in what the output says
	report := &network.PingReport{Sent: sent, LossPct: 100}
	if m := lossPattern.FindStringSubmatch(out); m != nil {
		report.LossPct, _ = strconv.ParseFloat(m[1], 64)
	}

	// The min, avg and max round trips only appear when some reply arrived
	if m := rttPattern.FindStringSubmatch(out); m != nil {
		report.RTTMin, report.RTTAvg, report.RTTMax = millis(m[1]), millis(m[2]), millis(m[3])
	}
	return report
}

// millis converts a decimal amount of milliseconds rounded like the Node bridge does
func millis(text string) *time.Duration {
	// An unreadable value is treated as missing
	v, err := strconv.ParseFloat(text, 64)
	if err != nil {
		return nil
	}

	// Whole milliseconds, rounded to the nearest one
	d := time.Duration(math.Round(v)) * time.Millisecond
	return &d
}

// parseHostWifi finds the line of the network in use, fields are IN-USE SSID SIGNAL FREQ CHAN
func parseHostWifi(out string) *network.HostWifi {
	for _, l := range lines(out) {
		// The network in use is the line marked with a star
		if !strings.HasPrefix(l, "*") {
			continue
		}

		// Five fields at least
		parts := strings.Split(l, ":")
		if len(parts) < 5 {
			return nil
		}
		// The SSID may contain colons, the last three fields never do
		n := len(parts)
		signal, _ := strconv.Atoi(parts[n-3])
		channel, _ := strconv.Atoi(parts[n-1])
		return &network.HostWifi{SSID: strings.Join(parts[1:n-3], ":"), Signal: signal, Freq: parts[n-2], Channel: channel}
	}
	return nil
}

// parseDefaultRoute reads the gateway and device of the default route
func parseDefaultRoute(out string) (netip.Addr, string, bool) {
	// default via <gateway> dev <device>
	m := routePattern.FindStringSubmatch(out)
	if m == nil {
		return netip.Addr{}, "", false
	}
	addr, err := netip.ParseAddr(m[1])
	return addr, m[2], err == nil
}

// wifiDevices returns the names of the Wi-Fi devices listed as DEVICE:TYPE
func wifiDevices(out string) []string {
	// Keep the devices whose type is wifi
	var devs []string
	for _, l := range lines(out) {
		if name, kind, ok := strings.Cut(l, ":"); ok && kind == "wifi" {
			devs = append(devs, name)
		}
	}
	return devs
}

// parseDHCPServer reads the DHCP server address among the DHCP options of a device
func parseDHCPServer(out string) (netip.Addr, bool) {
	// dhcp_server_identifier = <address>
	m := dhcpPattern.FindStringSubmatch(out)
	if m == nil {
		return netip.Addr{}, false
	}
	addr, err := netip.ParseAddr(m[1])
	return addr, err == nil && addr.Is4()
}

// parseWifiScan reads SSID SIGNAL FREQ SECURITY lines where a colon inside a value is escaped
func parseWifiScan(out string) []network.WifiNetwork {
	best := map[string]network.WifiNetwork{}
	for _, l := range lines(out) {
		// Hidden networks have no name and are skipped
		fields := splitEscaped(l)
		if len(fields) < 4 || fields[0] == "" {
			continue
		}

		// The frequency field reads like 2437 MHz
		signal, _ := strconv.Atoi(fields[1])
		freq, _ := strconv.Atoi(strings.Fields(fields[2] + " 0")[0])
		net := network.WifiNetwork{SSID: fields[0], Signal: signal, FreqMHz: freq, Security: fields[3], Band24: freq > 0 && freq < 3000}
		// Keep one entry per name: a 2.4 GHz access point wins, then the strongest signal
		prev, seen := best[net.SSID]
		if !seen || (net.Band24 && !prev.Band24) || (net.Band24 == prev.Band24 && net.Signal > prev.Signal) {
			best[net.SSID] = net
		}
	}

	// 2.4 GHz first, then by signal, then by name so the order is stable
	out2 := make([]network.WifiNetwork, 0, len(best))
	for _, n := range best {
		out2 = append(out2, n)
	}
	sort.Slice(out2, func(a, b int) bool {
		if out2[a].Band24 != out2[b].Band24 {
			return out2[a].Band24
		}
		if out2[a].Signal != out2[b].Signal {
			return out2[a].Signal > out2[b].Signal
		}
		return out2[a].SSID < out2[b].SSID
	})
	return out2
}

// splitEscaped splits an nmcli terse line on the colons that are not escaped
func splitEscaped(line string) []string {
	// Hide the escaped colons, split on the real ones, then restore the hidden ones
	protected := escapedColon.ReplaceAllString(line, nmcliSentinel)
	parts := strings.Split(protected, ":")
	for i, p := range parts {
		parts[i] = strings.ReplaceAll(p, nmcliSentinel, ":")
	}
	return parts
}
