package app

import (
	"github.com/AndresRamirez9912/photo-print-bridge/internal/adapters/outbound/cp4x"
	"github.com/AndresRamirez9912/photo-print-bridge/internal/adapters/outbound/hostnet"
	"github.com/AndresRamirez9912/photo-print-bridge/internal/core/services"
	"github.com/AndresRamirez9912/photo-print-bridge/internal/shared"
)

// Services groups the core services once their outbound adapters are injected
type Services struct {
	Activity    *services.ActivityService
	Printers    *services.PrinterService
	Photos      *services.PhotoService
	Diagnostics *services.DiagnosticsService
}

// newServices builds the outbound adapters and injects them into the core services
func newServices(cfg *AppConfig, logger *shared.Logger) *Services {
	// The activity service is the publisher every other service writes to
	activity := services.NewActivityService(logger.WithSection("activity"), cfg.Activity.LogBuffer)

	// The printer adapter writes every raw exchange to the protocol log
	driver := cp4x.NewDriver(printerAdapterConfig(cfg), activity.Log)
	printers := services.NewPrinterService(driver, activity, printerServiceConfig(cfg))

	// Photos print through the printer service and diagnostics read its state
	return &Services{
		Activity:    activity,
		Printers:    printers,
		Photos:      services.NewPhotoService(printers),
		Diagnostics: services.NewDiagnosticsService(hostnet.NewInspector(nil), printers),
	}
}

// printerAdapterConfig maps the printer section of the config to the cp4x adapter, zero values keep defaults
func printerAdapterConfig(cfg *AppConfig) cp4x.Config {
	p := cfg.Printer
	return cp4x.Config{
		// Ports, only different from the defaults with a simulator
		DataPort:      p.DataPort,
		CommandPort:   p.CommandPort,
		DiscoveryPort: p.DiscoveryPort,

		// Session timings
		WakeWindow:        p.WakeWindow,
		HeartbeatInterval: p.HeartbeatInterval,
		HeartbeatTimeout:  p.HeartbeatTimeout,

		// Job timings
		ClaimTimeout:    p.ClaimTimeout,
		PrintTimeout:    p.PrintTimeout,
		SendStall:       p.SendStall,
		SilentPrintDone: p.SilentPrintDone,
	}
}

// printerServiceConfig maps the printer section of the config to the printer service
func printerServiceConfig(cfg *AppConfig) services.PrinterConfig {
	// Start from the defaults and override only what the config sets
	out := services.DefaultPrinterConfig()

	// The wake window of the adapter is also the longest one a client may ask for
	if cfg.Printer.WakeWindow > 0 {
		out.MaxWake = cfg.Printer.WakeWindow
	}

	// Reconnection timings
	if cfg.Printer.ReconnectPause > 0 {
		out.ReconnectPause = cfg.Printer.ReconnectPause
	}
	if cfg.Printer.RetryWait > 0 {
		out.RetryWait = cfg.Printer.RetryWait
	}
	return out
}
