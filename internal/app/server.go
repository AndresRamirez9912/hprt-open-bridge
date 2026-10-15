package app

import (
	"context"
	"errors"
	"net"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/AndresRamirez9912/photo-print-bridge/internal/shared"
)

// Server is the REST server of the bridge with a graceful shutdown on SIGINT and SIGTERM
type Server struct {
	http            *http.Server
	logger          *shared.Logger
	shutdownTimeout time.Duration
	// cancelStreams ends the long lived event streams so the shutdown does not wait for them
	cancelStreams context.CancelFunc
}

// NewServer returns a server for handler on addr
func NewServer(addr string, handler http.Handler, shutdownTimeout time.Duration, logger *shared.Logger) *Server {
	// Every request context derives from streams, cancelling it ends the event streams
	streams, cancel := context.WithCancel(context.Background())

	srv := &http.Server{
		Addr:    addr,
		Handler: handler,
		// No write timeout: a print request answers when the printer finishes, about a minute later
		ReadHeaderTimeout: 10 * time.Second,
		IdleTimeout:       2 * time.Minute,
		BaseContext:       func(net.Listener) context.Context { return streams },
	}

	// Shutdown calls the registered functions in their own goroutines when it starts
	srv.RegisterOnShutdown(cancel)

	return &Server{http: srv, logger: logger, shutdownTimeout: shutdownTimeout, cancelStreams: cancel}
}

// Run serves until ctx ends or a stop signal arrives, then shuts the server down gracefully
func (s *Server) Run(ctx context.Context) error {
	// ctx is cancelled when SIGINT or SIGTERM arrives
	ctx, stop := signal.NotifyContext(ctx, os.Interrupt, syscall.SIGTERM)
	defer stop()

	// Open the port first so an address already in use fails here and not in the goroutine
	listener, err := net.Listen("tcp", s.http.Addr)
	if err != nil {
		return err
	}
	s.logger.Info("REST server listening", "addr", listener.Addr().String())

	// Serve blocks, so it runs in a goroutine that hands its result over a buffered channel
	// The buffer lets the goroutine finish even if nobody reads the channel any more
	serveErr := make(chan error, 1)
	go func() { serveErr <- s.http.Serve(listener) }()

	// Wait for whichever comes first: the server failing or the stop signal
	select {
	case err := <-serveErr:
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return err
	case <-ctx.Done():
	}

	// Graceful shutdown with its own time limit
	s.logger.Info("stop signal received, shutting down the REST server", "timeout", s.shutdownTimeout.String())
	shutdownCtx, cancel := context.WithTimeout(context.Background(), s.shutdownTimeout)
	defer cancel()
	// Shutdown stops accepting connections, cancels the event streams and waits for the running requests
	if err := s.http.Shutdown(shutdownCtx); err != nil {
		s.logger.Warn("REST server did not stop in time, closing the remaining connections", "error", err.Error())
		_ = s.http.Close()
	}

	// Make sure the streams context ends even if Shutdown returned early
	s.cancelStreams()
	s.logger.Info("REST server stopped")
	return nil
}
