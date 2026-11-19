// Package app assembles the API and manages its lifecycle.
package app

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"time"

	"github.com/2inxs/openbanking-service/internal/config"
	"github.com/2inxs/openbanking-service/internal/delivery/http/middleware"
)

// App owns an HTTP server. Create a new App for each run.
type App struct {
	server          *http.Server
	shutdownTimeout time.Duration
	logger          *slog.Logger
}

// New builds the API. Business routes will be added by the HTTP delivery layer.
func New(cfg config.Config, logger *slog.Logger) *App {
	if logger == nil {
		logger = slog.Default()
	}

	mux := http.NewServeMux()
	handler := middleware.RequestID(
		middleware.Logging(logger)(
			middleware.Recover(logger)(mux),
		),
	)

	return &App{
		server: &http.Server{
			Addr:              cfg.HTTPAddr,
			Handler:           handler,
			ReadHeaderTimeout: cfg.HTTPReadHeaderTimeout,
			ReadTimeout:       cfg.HTTPReadTimeout,
			WriteTimeout:      cfg.HTTPWriteTimeout,
			IdleTimeout:       cfg.HTTPIdleTimeout,
			MaxHeaderBytes:    1 << 20, // 1 MB
			ErrorLog:          slog.NewLogLogger(logger.Handler(), slog.LevelError),
		},
		shutdownTimeout: cfg.ShutdownTimeout,
		logger:          logger,
	}
}

// Run serves requests until cancellation or a server error.
// Cancellation stops accepting requests and lets in-flight requests finish.
func (a *App) Run(ctx context.Context) error {
	if ctx.Err() != nil {
		return nil
	}
	var listenConfig net.ListenConfig
	listener, err := listenConfig.Listen(ctx, "tcp", a.server.Addr)
	if err != nil {
		return fmt.Errorf("listen on HTTP address: %w", err)
	}
	return a.serve(ctx, listener)
}

func (a *App) serve(ctx context.Context, listener net.Listener) error {
	serveErrors := make(chan error, 1)
	go func() {
		serveErrors <- a.server.Serve(listener)
	}()
	a.logger.Info("HTTP server started", "address", listener.Addr().String())

	select {
	case err := <-serveErrors:
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return fmt.Errorf("serve HTTP: %w", err)
	case <-ctx.Done():
	}

	a.logger.Info("HTTP server stopping")
	// The signal context is already cancelled. Shutdown needs its own deadline.
	shutdownCtx, cancel := context.WithTimeout(context.Background(), a.shutdownTimeout)
	defer cancel()
	shutdownErr := a.server.Shutdown(shutdownCtx)
	if shutdownErr != nil {
		shutdownErr = errors.Join(shutdownErr, a.server.Close())
	}
	if err := <-serveErrors; !errors.Is(err, http.ErrServerClosed) {
		shutdownErr = errors.Join(shutdownErr, err)
	}
	if shutdownErr != nil {
		return fmt.Errorf("shut down HTTP server: %w", shutdownErr)
	}
	a.logger.Info("HTTP server stopped")
	return nil
}
