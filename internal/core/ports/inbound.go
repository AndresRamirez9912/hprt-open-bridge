package ports

import (
	"context"
	"net/netip"
	"time"

	"github.com/AndresRamirez9912/photo-print-bridge/internal/core/domains/network"
	"github.com/AndresRamirez9912/photo-print-bridge/internal/core/domains/printer"
)

// PrinterUseCase manages the connection with the printer
type PrinterUseCase interface {
	// Discover looks for printers for the given time, an invalid target means broadcast
	Discover(ctx context.Context, timeout time.Duration, target netip.Addr) ([]printer.Discovered, error)
	// Connect opens a session with a printer and keeps it alive until Disconnect
	Connect(ctx context.Context, req printer.ConnectRequest) (printer.State, error)
	// Disconnect closes the session and stops any reconnection
	Disconnect(ctx context.Context) printer.State
	// State returns the current snapshot of the connection
	State() printer.State
}

// PhotoUseCase prints photos on the connected printer
type PhotoUseCase interface {
	// PrintPhoto validates the file and prints it, waiting until the job ends
	PrintPhoto(ctx context.Context, data []byte) (printer.PrintResult, error)
	// CancelPrint aborts the running job and reports whether there was one
	CancelPrint(ctx context.Context) bool
}

// DiagnosticsUseCase measures the network between the bridge and the printer
type DiagnosticsUseCase interface {
	// LinkTest pings ip, or the connected printer when ip is invalid
	LinkTest(ctx context.Context, ip netip.Addr) (*network.LinkReport, error)
	// Gateway returns the router of the bridge machine
	Gateway(ctx context.Context) (*network.Gateway, error)
	// ScanWifi lists the Wi-Fi networks the bridge machine can see
	ScanWifi(ctx context.Context) ([]network.WifiNetwork, error)
}

// ActivityUseCase gives clients the live events and the protocol log
type ActivityUseCase interface {
	// Subscribe delivers live events until ctx ends, the channel is then closed
	Subscribe(ctx context.Context) <-chan printer.Event
	// Logs returns the recent protocol log, oldest first
	Logs() []printer.LogEntry
}
