package postgres

import (
	"context"
	"errors"
	"net"
	"strings"
	"testing"
	"time"

	"github.com/2inxs/openbanking-service/internal/config"
)

func settings() config.DatabaseConfig {
	return config.DatabaseConfig{
		URL: "postgres://postgres@localhost:5432/test?sslmode=disable", MaxConns: 2,
		ConnectTimeout: time.Second, StartupTimeout: time.Second, StatementTimeout: 30 * time.Second,
	}
}

func TestPoolConfiguration(t *testing.T) {
	cfg := settings()
	cfg.URL += "&application_name=custom&statement_timeout=0"
	cfg.StatementTimeout = 1500 * time.Microsecond
	parsed, err := poolConfig(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if parsed.MaxConns != 2 || parsed.ConnConfig.ConnectTimeout != time.Second {
		t.Fatal("pool limits were not applied")
	}
	if parsed.ConnConfig.RuntimeParams["statement_timeout"] != "2" || parsed.ConnConfig.RuntimeParams["application_name"] != "custom" {
		t.Fatal("statement timeout must round up to milliseconds; application_name must be preserved")
	}
}

func TestInvalidConfigDoesNotExposeCredentials(t *testing.T) {
	for _, value := range []string{
		"postgres://private-user:private-password@localhost:not-a-port/test",
		"host=localhost password='private-password", "",
	} {
		cfg := settings()
		cfg.URL = value
		_, err := Open(context.Background(), cfg)
		if !errors.Is(err, ErrInvalidConfig) {
			t.Fatalf("expected invalid configuration, got %v", err)
		}
		if strings.Contains(err.Error(), "private") || (value != "" && strings.Contains(err.Error(), value)) {
			t.Fatal("configuration error exposed connection credentials")
		}
	}
}

func TestStartupTimeoutDoesNotExposeCredentials(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	finished := make(chan struct{})
	defer close(finished)
	go func() {
		conn, err := listener.Accept()
		if err != nil {
			return
		}
		defer conn.Close()
		<-finished
	}()
	cfg := settings()
	cfg.URL = "postgres://private-user:private-password@" + listener.Addr().String() + "/test?sslmode=disable"
	cfg.StartupTimeout = 50 * time.Millisecond
	pool, err := Open(context.Background(), cfg)
	if pool != nil {
		pool.Close()
		t.Fatal("unreachable database returned a pool")
	}
	if !errors.Is(err, ErrUnavailable) || !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("expected safe startup timeout, got %v", err)
	}
	if strings.Contains(err.Error(), "private") || strings.Contains(err.Error(), cfg.URL) {
		t.Fatal("connection failure exposed credentials")
	}
}
