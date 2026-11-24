// Package app assembles the API and manages its lifecycle.
package app

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"sync"
	"time"

	"github.com/2inxs/openbanking-service/internal/config"
	"github.com/2inxs/openbanking-service/internal/delivery/http/middleware"
)

// App owns an HTTP server. Create a new App for each run.
type App struct {
	server          *http.Server
	shutdownTimeout time.Duration
	logger          *slog.Logger
	workers         []func(context.Context) error
}

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

// AddWorker регистрирует управляемую фоновую задачу, которая завершится при отмене ctx.
func (a *App) AddWorker(fn func(context.Context) error) {
	if fn != nil {
		a.workers = append(a.workers, fn)
	}
}

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
	workerCtx, cancelWorkers := context.WithCancel(ctx)
	defer cancelWorkers()

	var workersWG sync.WaitGroup
	for _, w := range a.workers {
		worker := w
		workersWG.Add(1)
		go func() {
			defer workersWG.Done()
			if err := worker(workerCtx); err != nil && !errors.Is(err, context.Canceled) {
				a.logger.Error("background worker stopped with error", "error", err)
			}
		}()
	}

	serveErrors := make(chan error, 1)
	go func() {
		serveErrors <- a.server.Serve(listener)
	}()
	a.logger.Info("HTTP server started", "address", listener.Addr().String())

	select {
	case err := <-serveErrors:
		cancelWorkers()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), a.shutdownTimeout)
		defer cancel()

		waitErr := a.waitWorkers(shutdownCtx, &workersWG)
		if errors.Is(err, http.ErrServerClosed) {
			return waitErr
		}
		return errors.Join(fmt.Errorf("serve HTTP: %w", err), waitErr)
	case <-ctx.Done():
	}

	a.logger.Info("HTTP server stopping")
	cancelWorkers()

	shutdownCtx, cancel := context.WithTimeout(context.Background(), a.shutdownTimeout)
	defer cancel()

	shutdownErr := a.server.Shutdown(shutdownCtx)
	if shutdownErr != nil {
		shutdownErr = errors.Join(shutdownErr, a.server.Close())
	}
	if err := <-serveErrors; !errors.Is(err, http.ErrServerClosed) {
		shutdownErr = errors.Join(shutdownErr, err)
	}

	if err := a.waitWorkers(shutdownCtx, &workersWG); err != nil {
		shutdownErr = errors.Join(shutdownErr, err)
	}

	if shutdownErr != nil {
		return fmt.Errorf("shut down HTTP server: %w", shutdownErr)
	}
	a.logger.Info("HTTP server stopped")
	return nil
}

func (a *App) waitWorkers(ctx context.Context, wg *sync.WaitGroup) error {
	done := make(chan struct{})
	go func() {
		wg.Wait()
		close(done)
	}()

	select {
	case <-done:
		return nil
	case <-ctx.Done():
		return fmt.Errorf("wait for background workers: %w", ctx.Err())
	}
}
