package postgres

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"time"

	"github.com/2inxs/openbanking-service/internal/config"
	"github.com/jackc/pgx/v5/pgxpool"
)

var (
	ErrInvalidConfig = errors.New("postgres: invalid connection configuration")
	ErrUnavailable   = errors.New("postgres: database unavailable")
)

func poolConfig(cfg config.DatabaseConfig) (*pgxpool.Config, error) {
	if cfg.URL == "" || cfg.MaxConns < 1 || cfg.ConnectTimeout <= 0 || cfg.StartupTimeout <= 0 || cfg.StatementTimeout <= 0 {
		return nil, ErrInvalidConfig
	}
	parsed, err := pgxpool.ParseConfig(cfg.URL)
	if err != nil {
		// Driver parse errors can contain the complete URL, including credentials.
		return nil, ErrInvalidConfig
	}
	parsed.MaxConns = cfg.MaxConns
	if parsed.MinConns > cfg.MaxConns || parsed.MinIdleConns > cfg.MaxConns {
		return nil, ErrInvalidConfig
	}
	parsed.ConnConfig.ConnectTimeout = cfg.ConnectTimeout
	if cfg.StatementTimeout > time.Duration(1<<31-1)*time.Millisecond {
		return nil, ErrInvalidConfig
	}
	parsed.ConnConfig.RuntimeParams["statement_timeout"] = strconv.FormatInt(int64((cfg.StatementTimeout-1)/time.Millisecond+1), 10)
	if parsed.ConnConfig.RuntimeParams["application_name"] == "" {
		parsed.ConnConfig.RuntimeParams["application_name"] = "openbanking-api"
	}
	return parsed, nil
}

// Open returns a pool only after a successful startup Ping. The caller owns Close.
func Open(ctx context.Context, cfg config.DatabaseConfig) (*pgxpool.Pool, error) {
	parsed, err := poolConfig(cfg)
	if err != nil {
		return nil, err
	}
	startupCtx, cancel := context.WithTimeout(ctx, cfg.StartupTimeout)
	defer cancel()
	pool, err := pgxpool.NewWithConfig(startupCtx, parsed)
	if err != nil {
		return nil, ErrInvalidConfig
	}
	if err := pool.Ping(startupCtx); err != nil {
		pool.Close()
		if startupCtx.Err() != nil {
			return nil, fmt.Errorf("%w: %w", ErrUnavailable, startupCtx.Err())
		}
		// Do not expose driver errors, connection strings or server diagnostics.
		return nil, ErrUnavailable
	}
	return pool, nil
}
