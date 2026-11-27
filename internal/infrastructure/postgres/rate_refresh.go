package postgres

import (
	"context"
	"errors"
	"github.com/2inxs/openbanking-service/internal/domain"
	"github.com/2inxs/openbanking-service/internal/usecase/currency"
	"github.com/jackc/pgx/v5"
	"time"
)

type refreshDB interface {
	DBTX
	txBeginner
}
type RateRefreshCoordinator struct{ db refreshDB }

var _ currency.RefreshCoordinator = (*RateRefreshCoordinator)(nil)

func NewRateRefreshCoordinator(db refreshDB) *RateRefreshCoordinator {
	return &RateRefreshCoordinator{db: db}
}
func validRefreshDuration(d time.Duration) bool { return d >= time.Microsecond }
func validRefreshLease(lease currency.RefreshLease) bool {
	return lease.Key != "" && lease.Version > 0 && validIDs(lease.Owner) == nil
}
func (c *RateRefreshCoordinator) Claim(ctx context.Context, key string, lease time.Duration) (currency.RefreshLease, bool, error) {
	if key == "" || !validRefreshDuration(lease) {
		return currency.RefreshLease{}, false, domain.ErrInvalidModel
	}
	var result currency.RefreshLease
	result.Key = key
	err := c.db.QueryRow(ctx, `INSERT INTO rate_refresh_leases AS l (job_key,owner,version,lease_until,next_attempt_at)
 VALUES ($1,gen_random_uuid(),1,clock_timestamp()+$2::bigint*interval '1 microsecond',clock_timestamp())
 ON CONFLICT (job_key) DO UPDATE SET owner=EXCLUDED.owner,version=l.version+1,lease_until=clock_timestamp()+$2::bigint*interval '1 microsecond'
 WHERE l.lease_until<=clock_timestamp() AND l.next_attempt_at<=clock_timestamp()
 RETURNING owner::text,version`, key, lease.Microseconds()).Scan(&result.Owner, &result.Version)
	if errors.Is(err, pgx.ErrNoRows) {
		return currency.RefreshLease{}, false, nil
	}
	if err != nil {
		return currency.RefreshLease{}, false, repositoryError(err, ErrUnavailable, domain.ErrInvalidModel)
	}
	return result, true, nil
}
func (c *RateRefreshCoordinator) Complete(ctx context.Context, lease currency.RefreshLease, daily domain.DailyRates, interval time.Duration) error {
	if !validRefreshDuration(interval) || !validRefreshLease(lease) {
		return domain.ErrInvalidModel
	}
	tx, err := c.db.Begin(ctx)
	if err != nil {
		return repositoryError(err, ErrUnavailable, domain.ErrInvalidModel)
	}
	defer func() {
		cleanup, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = tx.Rollback(cleanup)
	}()
	var version int64
	err = tx.QueryRow(ctx, `SELECT version FROM rate_refresh_leases WHERE job_key=$1 AND owner=$2::uuid AND version=$3 AND lease_until>clock_timestamp() FOR UPDATE`, lease.Key, lease.Owner, lease.Version).Scan(&version)
	if errors.Is(err, pgx.ErrNoRows) {
		return currency.ErrRefreshLeaseLost
	}
	if err != nil {
		return repositoryError(err, ErrUnavailable, domain.ErrInvalidModel)
	}
	if err := NewRateRepository(tx).SaveBatch(ctx, daily); err != nil {
		return err
	}
	tag, err := tx.Exec(ctx, `UPDATE rate_refresh_leases SET lease_until=clock_timestamp(),next_attempt_at=clock_timestamp()+$4::bigint*interval '1 microsecond',last_success_at=clock_timestamp()
 WHERE job_key=$1 AND owner=$2::uuid AND version=$3 AND lease_until>clock_timestamp()`, lease.Key, lease.Owner, lease.Version, interval.Microseconds())
	if err != nil {
		return repositoryError(err, ErrUnavailable, domain.ErrInvalidModel)
	}
	if tag.RowsAffected() != 1 {
		return currency.ErrRefreshLeaseLost
	}
	return repositoryError(tx.Commit(ctx), ErrUnavailable, domain.ErrInvalidModel)
}
func (c *RateRefreshCoordinator) Fail(ctx context.Context, lease currency.RefreshLease, retry time.Duration) error {
	if !validRefreshDuration(retry) || !validRefreshLease(lease) {
		return domain.ErrInvalidModel
	}
	_, err := c.db.Exec(ctx, `UPDATE rate_refresh_leases SET lease_until=clock_timestamp(),next_attempt_at=clock_timestamp()+$4::bigint*interval '1 microsecond'
	 WHERE job_key=$1 AND owner=$2::uuid AND version=$3 AND lease_until>clock_timestamp()`, lease.Key, lease.Owner, lease.Version, retry.Microseconds())
	return repositoryError(err, ErrUnavailable, domain.ErrInvalidModel)
}
