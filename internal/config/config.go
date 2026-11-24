// Package config loads and validates application settings from the environment.
package config

import (
	"fmt"
	"net"
	"os"
	"strconv"
	"strings"
	"time"
)

// Config contains the settings needed to start and stop the API.
type RatesConfig struct {
	RefreshInterval time.Duration
	RetryInterval   time.Duration
	RequestTimeout  time.Duration
}

type Config struct {
	Database              DatabaseConfig
	Rates                 RatesConfig
	HTTPAddr              string
	HTTPReadHeaderTimeout time.Duration
	HTTPReadTimeout       time.Duration
	HTTPWriteTimeout      time.Duration
	HTTPIdleTimeout       time.Duration
	ShutdownTimeout       time.Duration
	LogLevel              string
}

type DatabaseConfig struct {
	URL              string
	MaxConns         int32
	ConnectTimeout   time.Duration
	StartupTimeout   time.Duration
	StatementTimeout time.Duration
}

// Load reads environment variables. DATABASE_URL is required; other settings have defaults.
// It does not load a .env file; the process environment is the source of truth.
func Load() (Config, error) {
	cfg := Config{
		Database: DatabaseConfig{
			URL: os.Getenv("DATABASE_URL"), MaxConns: 10,
			ConnectTimeout: 5 * time.Second, StartupTimeout: 10 * time.Second,
			StatementTimeout: 30 * time.Second,
		},
		Rates: RatesConfig{
			RefreshInterval: time.Hour,
			RetryInterval:   5 * time.Minute,
			RequestTimeout:  10 * time.Second,
		},
		HTTPAddr:              "127.0.0.1:8080",
		HTTPReadHeaderTimeout: 5 * time.Second,
		HTTPReadTimeout:       15 * time.Second,
		HTTPWriteTimeout:      30 * time.Second,
		HTTPIdleTimeout:       60 * time.Second,
		ShutdownTimeout:       10 * time.Second,
		LogLevel:              "info",
	}

	if strings.TrimSpace(cfg.Database.URL) == "" {
		return Config{}, fmt.Errorf("DATABASE_URL is required")
	}
	if value := os.Getenv("DB_MAX_CONNS"); value != "" {
		n, err := strconv.ParseInt(value, 10, 32)
		if err != nil || n < 1 {
			return Config{}, fmt.Errorf("DB_MAX_CONNS must be a positive 32-bit integer")
		}
		cfg.Database.MaxConns = int32(n)
	}
	if value := os.Getenv("HTTP_ADDR"); value != "" {
		cfg.HTTPAddr = value
	}
	_, port, err := net.SplitHostPort(cfg.HTTPAddr)
	if err != nil {
		return Config{}, fmt.Errorf("HTTP_ADDR must be a host:port address: %w", err)
	}
	portNumber, err := strconv.Atoi(port)
	if err != nil || portNumber < 1 || portNumber > 65535 || strings.ContainsAny(cfg.HTTPAddr, " \t\r\n") {
		return Config{}, fmt.Errorf("HTTP_ADDR must contain a numeric port between 1 and 65535 and no whitespace")
	}

	if value := strings.TrimSpace(os.Getenv("LOG_LEVEL")); value != "" {
		normalized := strings.ToLower(value)
		switch normalized {
		case "debug", "info", "warn", "error":
			cfg.LogLevel = normalized
		default:
			return Config{}, fmt.Errorf("LOG_LEVEL must be one of debug, info, warn, error")
		}
	}

	for _, setting := range []struct {
		name   string
		target *time.Duration
	}{
		{"HTTP_READ_HEADER_TIMEOUT", &cfg.HTTPReadHeaderTimeout},
		{"HTTP_READ_TIMEOUT", &cfg.HTTPReadTimeout},
		{"HTTP_WRITE_TIMEOUT", &cfg.HTTPWriteTimeout},
		{"HTTP_IDLE_TIMEOUT", &cfg.HTTPIdleTimeout},
		{"SHUTDOWN_TIMEOUT", &cfg.ShutdownTimeout},
		{"DB_CONNECT_TIMEOUT", &cfg.Database.ConnectTimeout},
		{"DB_STARTUP_TIMEOUT", &cfg.Database.StartupTimeout},
		{"DB_STATEMENT_TIMEOUT", &cfg.Database.StatementTimeout},
		{"RATES_REFRESH_INTERVAL", &cfg.Rates.RefreshInterval},
		{"RATES_RETRY_INTERVAL", &cfg.Rates.RetryInterval},
		{"RATES_REQUEST_TIMEOUT", &cfg.Rates.RequestTimeout},
	} {
		value := os.Getenv(setting.name)
		if value == "" {
			continue
		}
		duration, err := time.ParseDuration(value)
		if err != nil {
			return Config{}, fmt.Errorf("%s must be a duration such as 5s: %w", setting.name, err)
		}
		if duration <= 0 {
			return Config{}, fmt.Errorf("%s must be greater than zero", setting.name)
		}
		*setting.target = duration
	}
	if cfg.Database.StatementTimeout > time.Duration(1<<31-1)*time.Millisecond {
		return Config{}, fmt.Errorf("DB_STATEMENT_TIMEOUT must not exceed 2147483647ms")
	}
	return cfg, nil
}
