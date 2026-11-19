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
	} {
		t.Setenv(name, "")
	}
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
	} {
		t.Setenv(name, value)
	}
	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	want := Config{
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
