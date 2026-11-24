package postgres

import (
	"context"
	"errors"

	"github.com/2inxs/openbanking-service/internal/domain"
	"github.com/2inxs/openbanking-service/internal/usecase/banksync"
	"github.com/jackc/pgx/v5"
)

type ConnectionRepository struct{ db DBTX }

var _ banksync.ConnectionRepository = (*ConnectionRepository)(nil)

func NewConnectionRepository(db DBTX) *ConnectionRepository { return &ConnectionRepository{db: db} }

const connectionColumns = `id::text, user_id::text, bank_id::text,
 COALESCE(external_customer_id, ''), status, last_synced_at, COALESCE(last_sync_error,''), version`

var connectionStatuses = map[domain.ConnectionStatusID]string{
	domain.ConnectionActive: "active", domain.ConnectionError: "error",
	domain.ConnectionDisconnected: "disconnected", domain.ConnectionPending: "pending",
}

func scanConnection(row pgx.Row) (domain.BankConnection, error) {
	var c domain.BankConnection
	var status string
	err := row.Scan(&c.ID, &c.UserID, &c.BankID, &c.CustomerID, &status, &c.LastSyncedAt, &c.LastSyncError, &c.Version)
	if err != nil {
		return c, repositoryError(err, banksync.ErrConnectionNotFound, banksync.ErrConnectionConflict)
	}
	for id, text := range connectionStatuses {
		if text == status {
			c.Status = id
		}
	}
	if err := c.Validate(); err != nil {
		return domain.BankConnection{}, ErrUnavailable
	}
	return c, nil
}

func (r *ConnectionRepository) Create(ctx context.Context, c domain.BankConnection) (domain.BankConnection, error) {
	if err := c.Validate(); err != nil {
		return domain.BankConnection{}, err
	}
	if c.Version != 0 {
		return domain.BankConnection{}, domain.ErrInvalidModel
	}
	if err := validIDs(string(c.ID), string(c.UserID), string(c.BankID)); err != nil {
		return domain.BankConnection{}, err
	}
	return scanConnection(r.db.QueryRow(ctx, `INSERT INTO bank_connections
 (id,user_id,bank_id,external_customer_id,status,last_synced_at,last_sync_error)
 VALUES ($1,$2,$3,NULLIF($4,''),$5,$6,NULLIF($7,'')) RETURNING `+connectionColumns,
		string(c.ID), string(c.UserID), string(c.BankID), string(c.CustomerID), connectionStatuses[c.Status], c.LastSyncedAt, c.LastSyncError))
}

func (r *ConnectionRepository) UpdateSyncState(ctx context.Context, user domain.UserID, id domain.BankConnectionID, version int64, state banksync.ConnectionSyncState) (domain.BankConnection, error) {
	if err := validIDs(string(user), string(id)); err != nil {
		return domain.BankConnection{}, err
	}
	if version < 0 || !state.Status.Valid() || (state.LastSyncedAt != nil && state.LastSyncedAt.IsZero()) {
		return domain.BankConnection{}, domain.ErrInvalidModel
	}
	c, err := scanConnection(r.db.QueryRow(ctx, `UPDATE bank_connections
 SET status=$4, last_synced_at=GREATEST(last_synced_at,$5::timestamptz),last_sync_error=NULLIF($6,'')
 WHERE user_id=$1 AND id=$2 AND version=$3 RETURNING `+connectionColumns,
		string(user), string(id), version, connectionStatuses[state.Status], state.LastSyncedAt, state.LastSyncError))
	if errors.Is(err, banksync.ErrConnectionNotFound) {
		if _, getErr := r.Get(ctx, user, id); getErr != nil {
			return domain.BankConnection{}, getErr
		}
		return domain.BankConnection{}, banksync.ErrConnectionConflict
	}
	return c, err
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
