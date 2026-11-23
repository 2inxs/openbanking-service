//go:build integration

package integration_test

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"testing"
	"time"

	"github.com/2inxs/openbanking-service/internal/app"
	"github.com/2inxs/openbanking-service/internal/config"
	"github.com/2inxs/openbanking-service/internal/infrastructure/postgres"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

func databaseConfig(t *testing.T) config.DatabaseConfig {
	t.Helper()
	connectionURL := os.Getenv("POSTGRES_TEST_URL")
	if connectionURL == "" {
		t.Skip("set POSTGRES_TEST_URL to run PostgreSQL integration tests")
	}
	return config.DatabaseConfig{URL: connectionURL, MaxConns: 2, ConnectTimeout: time.Second,
		StartupTimeout: 5 * time.Second, StatementTimeout: 50 * time.Millisecond}
}

func TestPoolLimitsTimeoutAndClose(t *testing.T) {
	cfg := databaseConfig(t)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	pool, err := postgres.Open(ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	first, err := pool.Acquire(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer first.Release()
	second, err := pool.Acquire(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer second.Release()
	waitCtx, stop := context.WithTimeout(ctx, 50*time.Millisecond)
	defer stop()
	third, err := pool.Acquire(waitCtx)
	if third != nil {
		third.Release()
		t.Fatal("pool exceeded DB_MAX_CONNS")
	}
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("expected pool wait timeout, got %v", err)
	}
	first.Release()
	second.Release()
	_, err = pool.Exec(ctx, "SELECT pg_sleep(1)")
	var serverError *pgconn.PgError
	if !errors.As(err, &serverError) || serverError.Code != "57014" {
		t.Fatalf("statement_timeout was not enforced: %v", err)
	}
	if err := pool.Ping(ctx); err != nil {
		t.Fatalf("pool unusable after statement timeout: %v", err)
	}
	pool.Close()
	if conn, err := pool.Acquire(ctx); err == nil {
		conn.Release()
		t.Fatal("closed pool allowed acquiring a connection")
	}
}

type startupWriter struct{ ready chan string }

func (w startupWriter) Write(data []byte) (int, error) {
	var record struct {
		Message string `json:"msg"`
		Address string `json:"address"`
	}
	if json.Unmarshal(data, &record) == nil && record.Message == "HTTP server started" {
		select {
		case w.ready <- record.Address:
		default:
		}
	}
	return len(data), nil
}

func TestApplicationClosesPool(t *testing.T) {
	for _, listenFailure := range []bool{false, true} {
		t.Run(strconv.FormatBool(listenFailure), func(t *testing.T) {
			db := databaseConfig(t)
			parsed, err := url.Parse(db.URL)
			if err != nil {
				t.Fatal("invalid POSTGRES_TEST_URL")
			}
			name := "issue7_test_" + strconv.FormatInt(time.Now().UnixNano(), 10)
			query := parsed.Query()
			query.Set("application_name", name)
			parsed.RawQuery = query.Encode()
			db.URL = parsed.String()
			cfg := config.Config{Database: db, HTTPAddr: "127.0.0.1:0", ShutdownTimeout: time.Second,
				HTTPReadHeaderTimeout: time.Second, HTTPReadTimeout: time.Second,
				HTTPWriteTimeout: time.Second, HTTPIdleTimeout: time.Second}
			if listenFailure {
				listener, err := net.Listen("tcp", "127.0.0.1:0")
				if err != nil {
					t.Fatal(err)
				}
				defer listener.Close()
				cfg.HTTPAddr = listener.Addr().String()
			}
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			writer := startupWriter{ready: make(chan string, 1)}
			log := slog.New(slog.NewJSONHandler(writer, nil))
			done := make(chan error, 1)
			go func() { done <- app.Run(ctx, cfg, log) }()
			if !listenFailure {
				select {
				case address := <-writer.ready:
					client := &http.Client{Timeout: time.Second}
					response, err := client.Get("http://" + address + "/")
					if err != nil {
						t.Fatal(err)
					}
					_, _ = io.Copy(io.Discard, response.Body)
					_ = response.Body.Close()
					if response.StatusCode != http.StatusNotFound {
						t.Fatalf("unexpected status %d", response.StatusCode)
					}
				case err := <-done:
					t.Fatalf("application stopped before HTTP startup: %v", err)
				case <-ctx.Done():
					t.Fatal("application did not start before deadline")
				}
				cancel()
			}
			select {
			case err := <-done:
				if listenFailure && err == nil {
					t.Fatal("expected occupied HTTP port error")
				}
				if !listenFailure && err != nil {
					t.Fatalf("shutdown failed: %v", err)
				}
			case <-time.After(5 * time.Second):
				t.Fatal("application did not stop")
			}
			inspectCtx, stop := context.WithTimeout(context.Background(), 3*time.Second)
			defer stop()
			observer, err := pgx.Connect(inspectCtx, os.Getenv("POSTGRES_TEST_URL"))
			if err != nil {
				t.Fatal("could not inspect PostgreSQL connections")
			}
			defer observer.Close(context.Background())
			for {
				var count int
				if err := observer.QueryRow(inspectCtx, "SELECT count(*) FROM pg_stat_activity WHERE application_name=$1", name).Scan(&count); err != nil {
					t.Fatal("could not inspect application connections")
				}
				if count == 0 {
					break
				}
				select {
				case <-inspectCtx.Done():
					t.Fatal("application left database connections open")
				case <-time.After(10 * time.Millisecond):
				}
			}
		})
	}
}

func TestApplicationSkipsStartupWhenCancelled(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := app.Run(ctx, config.Config{}, nil); err != nil {
		t.Fatal(err)
	}
}
