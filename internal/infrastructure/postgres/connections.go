package postgres

import (
	"context"

	"github.com/2inxs/openbanking-service/internal/domain"
	"github.com/2inxs/openbanking-service/internal/usecase/banksync"
	"github.com/jackc/pgx/v5"
)

type ConnectionRepository struct{ db DBTX }

var _ banksync.ConnectionRepository = (*ConnectionRepository)(nil)

func NewConnectionRepository(db DBTX) *ConnectionRepository { return &ConnectionRepository{db: db} }

const connectionColumns = `id::text, user_id::text, bank_id::text,
 COALESCE(external_customer_id, ''), status, last_synced_at`

func scanConnection(row pgx.Row) (domain.BankConnection, error) {
	var c domain.BankConnection
	var status string
	err := row.Scan(&c.ID, &c.UserID, &c.BankID, &c.CustomerID, &status, &c.LastSyncedAt)
	if err != nil {
		return c, repositoryError(err, banksync.ErrConnectionNotFound, banksync.ErrConnectionConflict)
	}
	statuses := map[string]domain.ConnectionStatusID{
		"active": domain.ConnectionActive, "error": domain.ConnectionError,
		"disconnected": domain.ConnectionDisconnected, "pending": domain.ConnectionPending,
	}
	c.Status = statuses[status]
	if err := c.Validate(); err != nil {
		return domain.BankConnection{}, ErrUnavailable
	}
	return c, nil
}

func (r *ConnectionRepository) Create(ctx context.Context, c domain.BankConnection) (domain.BankConnection, error) {
	if err := c.Validate(); err != nil {
		return domain.BankConnection{}, err
	}
	if err := validIDs(string(c.ID), string(c.UserID), string(c.BankID)); err != nil {
		return domain.BankConnection{}, err
	}
	statuses := map[domain.ConnectionStatusID]string{
		domain.ConnectionActive: "active", domain.ConnectionError: "error",
		domain.ConnectionDisconnected: "disconnected", domain.ConnectionPending: "pending",
	}
	return scanConnection(r.db.QueryRow(ctx, `INSERT INTO bank_connections
 (id,user_id,bank_id,external_customer_id,status,last_synced_at)
 VALUES ($1,$2,$3,NULLIF($4,''),$5,$6) RETURNING `+connectionColumns,
		string(c.ID), string(c.UserID), string(c.BankID), string(c.CustomerID), statuses[c.Status], c.LastSyncedAt))
}

func (r *ConnectionRepository) Get(ctx context.Context, user domain.UserID, id domain.BankConnectionID) (domain.BankConnection, error) {
	if err := validIDs(string(user), string(id)); err != nil {
		return domain.BankConnection{}, err
	}
	return scanConnection(r.db.QueryRow(ctx, `SELECT `+connectionColumns+` FROM bank_connections WHERE user_id=$1 AND id=$2`, string(user), string(id)))
}

func (r *ConnectionRepository) GetByBank(ctx context.Context, user domain.UserID, bank domain.BankID) (domain.BankConnection, error) {
	if err := validIDs(string(user), string(bank)); err != nil {
		return domain.BankConnection{}, err
	}
	return scanConnection(r.db.QueryRow(ctx, `SELECT `+connectionColumns+` FROM bank_connections WHERE user_id=$1 AND bank_id=$2`, string(user), string(bank)))
}
