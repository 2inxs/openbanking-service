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
type Config struct {
	HTTPAddr              string
	HTTPReadHeaderTimeout time.Duration
	HTTPReadTimeout       time.Duration
	HTTPWriteTimeout      time.Duration
	HTTPIdleTimeout       time.Duration
	ShutdownTimeout       time.Duration
}

// Load reads environment variables. Unset or empty variables use defaults.
// It does not load a .env file; the process environment is the source of truth.
func Load() (Config, error) {
	cfg := Config{
		HTTPAddr:              "127.0.0.1:8080",
		HTTPReadHeaderTimeout: 5 * time.Second,
		HTTPReadTimeout:       15 * time.Second,
		HTTPWriteTimeout:      30 * time.Second,
		HTTPIdleTimeout:       60 * time.Second,
		ShutdownTimeout:       10 * time.Second,
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

	for _, setting := range []struct {
		name   string
		target *time.Duration
	}{
		{"HTTP_READ_HEADER_TIMEOUT", &cfg.HTTPReadHeaderTimeout},
		{"HTTP_READ_TIMEOUT", &cfg.HTTPReadTimeout},
		{"HTTP_WRITE_TIMEOUT", &cfg.HTTPWriteTimeout},
		{"HTTP_IDLE_TIMEOUT", &cfg.HTTPIdleTimeout},
		{"SHUTDOWN_TIMEOUT", &cfg.ShutdownTimeout},
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
	return cfg, nil
}
