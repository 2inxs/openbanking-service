package config

import (
	"strings"
	"testing"
	"time"
)

func clearSettings(t *testing.T) {
	t.Helper()
	for _, name := range []string{
		"HTTP_ADDR", "HTTP_READ_HEADER_TIMEOUT", "HTTP_READ_TIMEOUT",
		"HTTP_WRITE_TIMEOUT", "HTTP_IDLE_TIMEOUT", "SHUTDOWN_TIMEOUT",
		"LOG_LEVEL",
		"DATABASE_URL", "DB_MAX_CONNS", "DB_CONNECT_TIMEOUT", "DB_STARTUP_TIMEOUT", "DB_STATEMENT_TIMEOUT",
		"RATES_REFRESH_INTERVAL", "RATES_RETRY_INTERVAL", "RATES_REQUEST_TIMEOUT",
	} {
		t.Setenv(name, "")
	}
	t.Setenv("DATABASE_URL", "postgres://postgres@localhost:5432/openBanking?sslmode=disable")
}

func TestLoadDefaults(t *testing.T) {
	clearSettings(t)
	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.HTTPAddr != "127.0.0.1:8080" || cfg.ShutdownTimeout != 10*time.Second || cfg.LogLevel != "info" {
		t.Fatalf("unexpected defaults: %+v", cfg)
	}
	if cfg.HTTPReadHeaderTimeout <= 0 || cfg.HTTPReadTimeout <= 0 || cfg.HTTPWriteTimeout <= 0 || cfg.HTTPIdleTimeout <= 0 {
		t.Fatalf("HTTP timeouts must be positive: %+v", cfg)
	}
	if cfg.Rates.RefreshInterval <= 0 || cfg.Rates.RetryInterval <= 0 || cfg.Rates.RequestTimeout <= 0 {
		t.Fatalf("Rates timeouts must be positive: %+v", cfg)
	}
}

func TestLoadOverrides(t *testing.T) {
	clearSettings(t)
	for name, value := range map[string]string{
		"HTTP_ADDR":                "[::1]:9090",
		"HTTP_READ_HEADER_TIMEOUT": "2s",
		"HTTP_READ_TIMEOUT":        "20s",
		"HTTP_WRITE_TIMEOUT":       "40s",
		"HTTP_IDLE_TIMEOUT":        "2m",
		"SHUTDOWN_TIMEOUT":         "3s",
		"LOG_LEVEL":                "debug",
		"RATES_REFRESH_INTERVAL":   "30m",
		"RATES_RETRY_INTERVAL":     "1m",
		"RATES_REQUEST_TIMEOUT":    "5s",
	} {
		t.Setenv(name, value)
	}
	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	want := Config{
		Database: DatabaseConfig{
			URL: "postgres://postgres@localhost:5432/openBanking?sslmode=disable", MaxConns: 10,
			ConnectTimeout: 5 * time.Second, StartupTimeout: 10 * time.Second, StatementTimeout: 30 * time.Second,
		},
		Rates: RatesConfig{
			RefreshInterval: 30 * time.Minute,
			RetryInterval:   time.Minute,
			RequestTimeout:  5 * time.Second,
		},
		HTTPAddr:              "[::1]:9090",
		HTTPReadHeaderTimeout: 2 * time.Second,
		HTTPReadTimeout:       20 * time.Second,
		HTTPWriteTimeout:      40 * time.Second,
		HTTPIdleTimeout:       2 * time.Minute,
		ShutdownTimeout:       3 * time.Second,
		LogLevel:              "debug",
	}
	if cfg != want {
		t.Fatalf("Load() = %+v, want %+v", cfg, want)
	}
}

func TestLoadRejectsInvalidAddress(t *testing.T) {
	for _, address := range []string{"localhost", "localhost:http", "localhost:0", "localhost:65536", "localhost:-1", "bad host:8080", "http://localhost:8080"} {
		t.Run(address, func(t *testing.T) {
			clearSettings(t)
			t.Setenv("HTTP_ADDR", address)
			_, err := Load()
			if err == nil || !strings.Contains(err.Error(), "HTTP_ADDR") {
				t.Fatalf("expected HTTP_ADDR error, got %v", err)
			}
		})
	}
}

func TestLoadRejectsInvalidTimeouts(t *testing.T) {
	for _, name := range []string{
		"HTTP_READ_HEADER_TIMEOUT", "HTTP_READ_TIMEOUT", "HTTP_WRITE_TIMEOUT",
		"HTTP_IDLE_TIMEOUT", "SHUTDOWN_TIMEOUT",
		"DB_CONNECT_TIMEOUT", "DB_STARTUP_TIMEOUT", "DB_STATEMENT_TIMEOUT",
		"RATES_REFRESH_INTERVAL", "RATES_RETRY_INTERVAL", "RATES_REQUEST_TIMEOUT",
	} {
		for _, value := range []string{"0s", "-1s", "five", "5"} {
			t.Run(name+"/"+value, func(t *testing.T) {
				clearSettings(t)
				t.Setenv(name, value)
				_, err := Load()
				if err == nil || !strings.Contains(err.Error(), name) {
					t.Fatalf("expected %s error, got %v", name, err)
				}
			})
		}
	}
}

func TestLoadRejectsInvalidLogLevel(t *testing.T) {
	for _, level := range []string{"trace", "verbose", "123"} {
		t.Run(level, func(t *testing.T) {
			clearSettings(t)
			t.Setenv("LOG_LEVEL", level)
			_, err := Load()
			if err == nil || !strings.Contains(err.Error(), "LOG_LEVEL") {
				t.Fatalf("expected LOG_LEVEL error, got %v", err)
			}
		})
	}
}

func TestDatabaseSettings(t *testing.T) {
	clearSettings(t)
	t.Setenv("DB_MAX_CONNS", "3")
	t.Setenv("DB_CONNECT_TIMEOUT", "2s")
	t.Setenv("DB_STARTUP_TIMEOUT", "4s")
	t.Setenv("DB_STATEMENT_TIMEOUT", "15s")
	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Database.MaxConns != 3 || cfg.Database.ConnectTimeout != 2*time.Second || cfg.Database.StartupTimeout != 4*time.Second || cfg.Database.StatementTimeout != 15*time.Second {
		t.Fatal("database overrides were not applied")
	}
	for _, value := range []string{"0", "-1", "many", "2147483648"} {
		t.Setenv("DB_MAX_CONNS", value)
		if _, err := Load(); err == nil || !strings.Contains(err.Error(), "DB_MAX_CONNS") {
			t.Fatalf("expected DB_MAX_CONNS validation error, got %v", err)
		}
	}
}

func TestDatabaseURLRequired(t *testing.T) {
	clearSettings(t)
	t.Setenv("DATABASE_URL", "")
	if _, err := Load(); err == nil || !strings.Contains(err.Error(), "DATABASE_URL") {
		t.Fatalf("expected required DATABASE_URL error, got %v", err)
	}
}

func TestStatementTimeoutFitsPostgreSQL(t *testing.T) {
	clearSettings(t)
	t.Setenv("DB_STATEMENT_TIMEOUT", "1000h")
	if _, err := Load(); err == nil || !strings.Contains(err.Error(), "DB_STATEMENT_TIMEOUT") {
		t.Fatalf("expected PostgreSQL timeout limit error, got %v", err)
	}
}
