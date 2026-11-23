package app

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net"
	"net/http"
	"testing"
	"time"

	"github.com/2inxs/openbanking-service/internal/config"
)

func testApp() *App {
	return New(config.Config{
		HTTPAddr:              "127.0.0.1:0",
		HTTPReadHeaderTimeout: time.Second,
		HTTPReadTimeout:       time.Second,
		HTTPWriteTimeout:      time.Second,
		HTTPIdleTimeout:       time.Second,
		ShutdownTimeout:       time.Second,
	}, slog.New(slog.NewTextHandler(io.Discard, nil)))
}

func testListener(t *testing.T) net.Listener {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = listener.Close() })
	return listener
}

func testClient(t *testing.T) *http.Client {
	t.Helper()
	transport := &http.Transport{DisableKeepAlives: true}
	t.Cleanup(transport.CloseIdleConnections)
	return &http.Client{Transport: transport, Timeout: 5 * time.Second}
}

func waitFor[T any](t *testing.T, ch <-chan T) T {
	t.Helper()
	select {
	case result := <-ch:
		return result
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for server or request")
		var zero T
		return zero
	}
}

func startServing(t *testing.T, a *App, listener net.Listener) (context.CancelFunc, <-chan error) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	done := make(chan error, 1)
	go func() { done <- a.serve(ctx, listener) }()
	return cancel, done
}

func TestServerStartsAndStops(t *testing.T) {
	a := testApp()
	listener := testListener(t)
	cancel, done := startServing(t, a, listener)
	response, err := testClient(t).Get("http://" + listener.Addr().String() + "/")
	if err != nil {
		t.Fatal(err)
	}
	_ = response.Body.Close()
	if response.StatusCode != http.StatusNotFound {
		t.Fatalf("unconfigured route returned %d, want 404", response.StatusCode)
	}
	cancel()
	if err := waitFor(t, done); err != nil {
		t.Fatalf("normal shutdown failed: %v", err)
	}
}

func TestShutdownWaitsForInflightRequest(t *testing.T) {
	a := testApp()
	entered := make(chan struct{})
	release := make(chan struct{})
	defer close(release)
	a.server.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		close(entered)
		<-release
		w.WriteHeader(http.StatusNoContent)
	})
	stopping := make(chan struct{})
	a.server.RegisterOnShutdown(func() { close(stopping) })
	listener := testListener(t)
	cancel, done := startServing(t, a, listener)
	type requestResult struct {
		status int
		err    error
	}
	requestDone := make(chan requestResult, 1)
	client := testClient(t)
	go func() {
		response, err := client.Get("http://" + listener.Addr().String())
		if err != nil {
			requestDone <- requestResult{err: err}
			return
		}
		_ = response.Body.Close()
		requestDone <- requestResult{status: response.StatusCode}
	}()
	waitFor(t, entered)
	cancel()
	waitFor(t, stopping)
	select {
	case err := <-done:
		t.Fatalf("server stopped before the request finished: %v", err)
	default:
	}
	// Let the handler finish without closing release twice in deferred cleanup.
	release <- struct{}{}
	result := waitFor(t, requestDone)
	if result.err != nil || result.status != http.StatusNoContent {
		t.Fatalf("in-flight request did not finish normally: %+v", result)
	}
	if err := waitFor(t, done); err != nil {
		t.Fatalf("graceful shutdown failed: %v", err)
	}
}

func TestShutdownDeadlineClosesActiveConnection(t *testing.T) {
	a := testApp()
	a.shutdownTimeout = 50 * time.Millisecond
	entered := make(chan struct{})
	requestCancelled := make(chan struct{})
	a.server.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		close(entered)
		<-r.Context().Done()
		close(requestCancelled)
	})
	listener := testListener(t)
	cancel, done := startServing(t, a, listener)
	requestDone := make(chan error, 1)
	client := testClient(t)
	go func() {
		response, err := client.Get("http://" + listener.Addr().String())
		if response != nil {
			_ = response.Body.Close()
		}
		requestDone <- err
	}()
	waitFor(t, entered)
	cancel()
	if err := waitFor(t, done); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("expected shutdown deadline error, got %v", err)
	}
	waitFor(t, requestCancelled)
	if err := waitFor(t, requestDone); err == nil {
		t.Fatal("expected the unfinished request connection to be closed")
	}
}

func TestRunReturnsListenError(t *testing.T) {
	occupied := testListener(t)
	a := testApp()
	a.server.Addr = occupied.Addr().String()
	if err := a.Run(context.Background()); err == nil {
		t.Fatal("expected an error when the port is already occupied")
	}
}

func TestRunWithCancelledContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := testApp().Run(ctx); err != nil {
		t.Fatalf("cancelled startup returned an error: %v", err)
	}
}
func TestShutdownWaitsForBackgroundWorker(t *testing.T) {
	a := testApp()
	workerStarted := make(chan struct{})
	workerStopped := make(chan struct{})

	a.AddWorker(func(ctx context.Context) error {
		close(workerStarted)
		<-ctx.Done()
		time.Sleep(20 * time.Millisecond)
		close(workerStopped)
		return nil
	})

	listener := testListener(t)
	cancel, done := startServing(t, a, listener)

	waitFor(t, workerStarted)
	cancel()

	if err := waitFor(t, done); err != nil {
		t.Fatalf("shutdown failed: %v", err)
	}
	select {
	case <-workerStopped:
	default:
		t.Fatal("App.Run returned before background worker finished")
	}
}
