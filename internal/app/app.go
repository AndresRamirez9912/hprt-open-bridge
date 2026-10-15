package app

import (
	"context"

	httpadapter "github.com/AndresRamirez9912/photo-print-bridge/internal/adapters/inbound/http"
	"github.com/AndresRamirez9912/photo-print-bridge/internal/adapters/inbound/http/middleware"
	"github.com/AndresRamirez9912/photo-print-bridge/internal/shared"
)

// App is the bridge once every dependency is wired
type App struct {
	cfg      *AppConfig
	logger   *shared.Logger
	services *Services
	server   *Server
}

// New wires the adapters, the services, the middleware chain and the REST server
func New(cfg *AppConfig) *App {
	// Logger of the whole application, every component gets its own section
	logger := shared.NewLogger(cfg.App.Name, shared.ParseLevel(cfg.Logger.Level), cfg.Logger.AsJSON, cfg.Logger.NoColor)

	// Core services with their outbound adapters already injected
	svc := newServices(cfg, logger)

	// Inbound adapter: the controllers drive the use cases of the services
	handler := httpadapter.NewHandler(svc.Printers, svc.Photos, svc.Diagnostics, svc.Activity)

	// Each link handles the request and passes it to the next one, in this order
	chain := middleware.NewChain(
		middleware.NewRecovery(logger.WithSection("http")),
		middleware.NewRequestID(),
		middleware.NewLogging(logger.WithSection("http")),
		middleware.NewCORS(cfg.CORS.Enabled, cfg.CORS.AllowedOrigins, cfg.CORS.AllowedMethods, cfg.CORS.AllowedHeaders),
	)
	logger.Info("middleware chain ready", "links", chain.Names())

	// Router built from the route table and wrapped by the chain
	router := httpadapter.NewRouter(handler, chain)

	return &App{
		cfg:      cfg,
		logger:   logger,
		services: svc,
		server:   NewServer(cfg.App.RestAddr, router, cfg.App.ShutdownTimeout, logger.WithSection("server")),
	}
}

// Run serves the API until a stop signal arrives, then closes the printer session
func (a *App) Run(ctx context.Context) error {
	// Serve until a stop signal arrives
	a.logger.Info("starting", "env", a.cfg.App.Env)
	err := a.server.Run(ctx)

	// After the REST server stopped, close the printer session so the printer is released
	a.services.Printers.Disconnect(context.Background())
	a.logger.Info("stopped")
	return err
}
