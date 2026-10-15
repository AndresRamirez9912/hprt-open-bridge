package hostnet

import (
	"context"
	"net/netip"
	"os/exec"
	"strconv"
	"strings"
	"time"

	"github.com/AndresRamirez9912/photo-print-bridge/internal/core/domains/network"
	"github.com/AndresRamirez9912/photo-print-bridge/internal/core/ports"
)

// Compile time check that the adapter implements the outbound port
var _ ports.NetworkInspector = &Inspector{}

// Runner executes a system command and returns its standard output
type Runner func(ctx context.Context, timeout time.Duration, name string, args ...string) (string, error)

// Inspector implements ports.NetworkInspector with the ping, ip and nmcli commands of Linux
type Inspector struct {
	run Runner
}

// NewInspector returns an inspector, a nil runner uses the real system commands
func NewInspector(run Runner) *Inspector {
	if run == nil {
		run = runCommand
	}
	return &Inspector{run: run}
}

// runCommand runs a system command with a timeout, partial output is kept when it fails
func runCommand(ctx context.Context, timeout time.Duration, name string, args ...string) (string, error) {
	// The command is killed when the timeout or the caller context ends
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	out, err := exec.CommandContext(ctx, name, args...).Output()

	// ping exits with an error when packets are lost but its summary is still useful
	if err != nil && len(out) == 0 {
		return "", err
	}
	return string(out), nil
}

// pingCount is how many ICMP requests a link test sends
const pingCount = 20

// Ping sends a quick ICMP series to ip and summarizes it, nil when ping is not available
func (i *Inspector) Ping(ctx context.Context, ip netip.Addr) *network.PingReport {
	// Numeric output, summary only, one request every 250 ms with 2 s to answer each
	out, err := i.run(ctx, 15*time.Second, "ping", "-n", "-q", "-c", strconv.Itoa(pingCount), "-i", "0.25", "-W", "2", ip.String())
	if err != nil {
		return nil
	}
	return parsePing(out, pingCount)
}

// HostWifi returns the Wi-Fi network this machine is associated with, nil when there is none
func (i *Inspector) HostWifi(ctx context.Context) *network.HostWifi {
	// Terse output of the known networks without a new scan, the one in use is marked
	out, err := i.run(ctx, 5*time.Second, "nmcli", "-t", "-e", "no", "-f", "IN-USE,SSID,SIGNAL,FREQ,CHAN", "dev", "wifi", "list", "--rescan", "no")
	if err != nil {
		return nil
	}
	return parseHostWifi(out)
}

// Gateway returns the default route or, in hotspot mode, the DHCP server of the Wi-Fi device
//
// The printer hotspot sends no router option, but NetworkManager records the DHCP server
// address and that server is the printer itself
func (i *Inspector) Gateway(ctx context.Context) (*network.Gateway, error) {
	// The Wi-Fi network is reported with the gateway in both cases
	wifi := i.HostWifi(ctx)

	// A normal network has a default route
	if out, err := i.run(ctx, 3*time.Second, "ip", "-4", "route", "show", "default"); err == nil {
		if addr, dev, ok := parseDefaultRoute(out); ok {
			return &network.Gateway{Address: addr, Device: dev, Source: "default route", Wifi: wifi}, nil
		}
	}

	// In hotspot mode look for the DHCP server of each Wi-Fi device
	devices, _ := i.run(ctx, 3*time.Second, "nmcli", "-t", "-f", "DEVICE,TYPE", "device")
	for _, dev := range wifiDevices(devices) {
		opts, err := i.run(ctx, 3*time.Second, "nmcli", "-g", "DHCP4.OPTION", "device", "show", dev)
		if err != nil {
			continue
		}
		if addr, ok := parseDHCPServer(opts); ok {
			return &network.Gateway{Address: addr, Device: dev, Source: "dhcp server", Wifi: wifi}, nil
		}
	}
	return nil, network.ErrNoWifiNetwork
}

// ScanWifi lists the networks seen by this machine, the best one of each name, 2.4 GHz first
func (i *Inspector) ScanWifi(ctx context.Context) ([]network.WifiNetwork, error) {
	// A fresh scan of the radio, colons inside the values are escaped
	out, err := i.run(ctx, 20*time.Second, "nmcli", "-t", "-e", "yes", "-f", "SSID,SIGNAL,FREQ,SECURITY", "dev", "wifi", "list", "--rescan", "yes")
	if err != nil {
		return nil, network.ErrScanUnavailable
	}
	return parseWifiScan(out), nil
}

// lines splits command output in lines without the empty ones
func lines(out string) []string {
	// Split and drop the empty lines and the Windows line endings
	var res []string
	for _, l := range strings.Split(out, "\n") {
		if l = strings.TrimRight(l, "\r"); l != "" {
			res = append(res, l)
		}
	}
	return res
}
