package postgres

import (
	"context"
	"errors"

	"github.com/2inxs/openbanking-service/internal/domain"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"
)

// DBTX allows the same repositories to participate in a caller-owned pgx transaction.
type DBTX interface {
	Exec(context.Context, string, ...any) (pgconn.CommandTag, error)
	Query(context.Context, string, ...any) (pgx.Rows, error)
	QueryRow(context.Context, string, ...any) pgx.Row
}

func validIDs(ids ...string) error {
	for _, id := range ids {
		var uuid pgtype.UUID
		if err := uuid.Scan(id); err != nil || !uuid.Valid {
			return domain.ErrInvalidModel
		}
	}
	return nil
}

func repositoryError(err, notFound, conflict error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, pgx.ErrNoRows) {
		return notFound
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return err
	}
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) {
		switch pgErr.Code {
		case "23505", "23503", "23514":
			return conflict
		case "22003", "22001", "22P02":
			return domain.ErrInvalidModel
		}
	}
	// Server diagnostics may include bank identifiers and personal data.
	return ErrUnavailable
}

type txBeginner interface {
	Begin(ctx context.Context) (pgx.Tx, error)
}
