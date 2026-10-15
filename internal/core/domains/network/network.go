package network

import (
	"net/http"
	"net/netip"
	"time"

	"github.com/AndresRamirez9912/photo-print-bridge/internal/core/domains/printer"
	"github.com/AndresRamirez9912/photo-print-bridge/internal/shared"
)

// module is the name reported in every error of this domain
const module = "network"

// Errors of the network diagnostics domain
var (
	ErrScanUnavailable = shared.NewError(module, 3001, "Wi-Fi scan not available on this machine", http.StatusServiceUnavailable)
	ErrNoWifiNetwork   = shared.NewError(module, 3002, "This PC has no IPv4 network on Wi-Fi yet", http.StatusNotFound)
)

// PingReport is the result of an ICMP ping series from the bridge machine to the printer
type PingReport struct {
	Sent    int
	LossPct float64
	// RTTMin, RTTAvg and RTTMax are nil when no reply arrived
	RTTMin *time.Duration
	RTTAvg *time.Duration
	RTTMax *time.Duration
}

// HostWifi is the Wi-Fi network the bridge machine is associated with
type HostWifi struct {
	SSID    string
	Signal  int
	Freq    string
	Channel int
}

// Gateway is the router of the network the bridge machine is on, in hotspot mode the printer itself
type Gateway struct {
	Address netip.Addr
	Device  string
	Source  string
	Wifi    *HostWifi
}

// WifiNetwork is a Wi-Fi network seen from the bridge machine
type WifiNetwork struct {
	SSID     string
	Signal   int
	FreqMHz  int
	Security string
	// Band24 marks the 2.4 GHz networks, the only ones the printer can join
	Band24 bool
}

// LinkReport gathers every measure of the link between the bridge machine and a printer
type LinkReport struct {
	IP        netip.Addr
	Ping      *PingReport
	HostWifi  *HostWifi
	Heartbeat *printer.LinkStats
}
