package ports

import (
	"context"
	"net/netip"
	"time"

	"github.com/AndresRamirez9912/photo-print-bridge/internal/core/domains/network"
	"github.com/AndresRamirez9912/photo-print-bridge/internal/core/domains/photo"
	"github.com/AndresRamirez9912/photo-print-bridge/internal/core/domains/printer"
)

// PrinterDriver finds printers on the local network and opens sessions with them
type PrinterDriver interface {
	// Discover probes the network until ctx ends, an invalid target means broadcast
	Discover(ctx context.Context, target netip.Addr) ([]printer.Discovered, error)
	// Connect opens a session, waking the printer for up to wake or the default when zero
	Connect(ctx context.Context, ip netip.Addr, wake time.Duration) (PrinterSession, error)
}

// PrinterMonitor reads what an open session knows about its printer
type PrinterMonitor interface {
	// Address returns the IPv4 address of the printer
	Address() netip.Addr
	// Status returns the last status block, ok is false until the first one arrives
	Status() (status printer.Status, ok bool)
	// Link returns the heartbeat statistics, nil before the first heartbeat
	Link() *printer.LinkStats
	// Events delivers status changes, print progress and the loss of the connection
	Events() <-chan printer.SessionEvent
	// Done is closed when the session ends for any reason
	Done() <-chan struct{}
}

// PrinterCommander sends commands and print jobs to an open session
type PrinterCommander interface {
	// SerialNumber asks the printer for its serial number
	SerialNumber(ctx context.Context) (string, error)
	// FirmwareVersion asks the printer for its full firmware version
	FirmwareVersion(ctx context.Context) (string, error)
	// Print reserves the printer, sends the photo and waits until it is printed or fails
	Print(ctx context.Context, p *photo.Photo) (printer.PrintResult, error)
	// Close ends the session and can be called more than once
	Close() error
}

// PrinterSession is one open connection with one printer
type PrinterSession interface {
	PrinterMonitor
	PrinterCommander
}

// NetworkInspector measures the network of the machine that runs the bridge
type NetworkInspector interface {
	// Ping sends an ICMP series to ip, nil when ping is not available
	Ping(ctx context.Context, ip netip.Addr) *network.PingReport
	// HostWifi returns the Wi-Fi network of this machine, nil when there is none
	HostWifi(ctx context.Context) *network.HostWifi
	// Gateway returns the router of this machine or the DHCP server in hotspot mode
	Gateway(ctx context.Context) (*network.Gateway, error)
	// ScanWifi lists the Wi-Fi networks this machine can see
	ScanWifi(ctx context.Context) ([]network.WifiNetwork, error)
}
