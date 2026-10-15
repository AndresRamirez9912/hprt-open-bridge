package services

import (
	"context"
	"net/netip"
	"sync"

	"github.com/AndresRamirez9912/photo-print-bridge/internal/core/domains/network"
	"github.com/AndresRamirez9912/photo-print-bridge/internal/core/domains/printer"
	"github.com/AndresRamirez9912/photo-print-bridge/internal/core/ports"
)

// Compile time check that the service implements its inbound port
var _ ports.DiagnosticsUseCase = &DiagnosticsService{}

// DiagnosticsService measures the network between the bridge machine and the printer
type DiagnosticsService struct {
	inspector ports.NetworkInspector
	printers  ports.PrinterUseCase
}

// NewDiagnosticsService returns a service that measures with inspector
func NewDiagnosticsService(inspector ports.NetworkInspector, printers ports.PrinterUseCase) *DiagnosticsService {
	return &DiagnosticsService{inspector: inspector, printers: printers}
}

// LinkTest pings ip, or the connected printer when ip is invalid, and adds the heartbeat statistics
func (s *DiagnosticsService) LinkTest(ctx context.Context, ip netip.Addr) (*network.LinkReport, error) {
	// Without an explicit address, measure the connected printer
	state := s.printers.State()
	if !ip.IsValid() {
		ip = state.IP
	}
	if !ip.Is4() {
		return nil, printer.ErrNoPrinterIP
	}

	// The ping series takes seconds, so the Wi-Fi query runs at the same time in a second goroutine
	// Each goroutine writes a different field of report and the WaitGroup waits for both
	report := &network.LinkReport{IP: ip}
	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		report.Ping = s.inspector.Ping(ctx, ip)
	}()
	go func() {
		defer wg.Done()
		report.HostWifi = s.inspector.HostWifi(ctx)
	}()
	wg.Wait()

	// The heartbeat statistics only describe the printer the session is connected to
	if state.IP == ip {
		report.Heartbeat = state.Link
	}
	return report, nil
}

// Gateway returns the router of the bridge machine, the printer itself in hotspot mode
func (s *DiagnosticsService) Gateway(ctx context.Context) (*network.Gateway, error) {
	return s.inspector.Gateway(ctx)
}

// ScanWifi lists the Wi-Fi networks the bridge machine can see
func (s *DiagnosticsService) ScanWifi(ctx context.Context) ([]network.WifiNetwork, error) {
	return s.inspector.ScanWifi(ctx)
}
