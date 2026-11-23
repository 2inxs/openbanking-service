package postgres

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/2inxs/openbanking-service/internal/domain"
	"github.com/2inxs/openbanking-service/internal/domain/money"
	"github.com/2inxs/openbanking-service/internal/usecase/account"
	"github.com/2inxs/openbanking-service/internal/usecase/banksync"
	"github.com/jackc/pgx/v5"
)

type AccountRepository struct{ db DBTX }

var (
	_ account.Repository         = (*AccountRepository)(nil)
	_ banksync.AccountRepository = (*AccountRepository)(nil)
)

func NewAccountRepository(db DBTX) *AccountRepository { return &AccountRepository{db: db} }

const accountColumns = `id::text, user_id::text, bank_id::text,
 COALESCE(bank_connection_id::text,''), COALESCE(external_account_id,''),
 COALESCE(account_number,''), name, type, status, currency_code,
 reported_balance::text, balance_as_of`

var accountTypes = map[domain.AccountTypeID]string{
	domain.AccountCurrent: "current", domain.AccountCreditCard: "credit_card",
	domain.AccountSavings: "savings", domain.AccountDeposit: "deposit", domain.AccountTypeUnknown: "unknown",
}

var accountStatuses = map[domain.AccountStatusID]string{
	domain.AccountActive: "active", domain.AccountClosed: "closed", domain.AccountStatusUnknown: "unknown",
}

func scanAccount(row pgx.Row) (domain.Account, error) {
	var a domain.Account
	var typ, status string
	var balance *string
	var asOf *time.Time
	if err := row.Scan(&a.ID, &a.UserID, &a.BankID, &a.ConnectionID, &a.ExternalAccountID,
		&a.Number, &a.Name, &typ, &status, &a.Currency, &balance, &asOf); err != nil {
		return a, repositoryError(err, account.ErrNotFound, account.ErrConflict)
	}
	for id, text := range accountTypes {
		if text == typ {
			a.Type = id
		}
	}
	for id, text := range accountStatuses {
		if text == status {
			a.Status = id
		}
	}
	if balance != nil {
		amount, err := money.Parse(*balance)
		if err != nil || asOf == nil {
			return domain.Account{}, ErrUnavailable
		}
		a.Balance = &domain.BalanceSnapshot{Value: money.Money{Amount: amount, Currency: a.Currency}, AsOf: *asOf}
	}
	if err := a.Validate(); err != nil {
		return domain.Account{}, ErrUnavailable
	}
	return a, nil
}

func validateAccount(a domain.Account) error {
	if err := a.Validate(); err != nil {
		return err
	}
	if err := validIDs(string(a.ID), string(a.UserID), string(a.BankID)); err != nil {
		return err
	}
	if a.ConnectionID != "" {
		if err := validIDs(string(a.ConnectionID)); err != nil {
			return err
		}
	}
	if a.Balance != nil {
		return validateBalance(*a.Balance)
	}
	return nil
}

func validateBalance(b domain.BalanceSnapshot) error {
	if err := b.Value.Validate(); err != nil {
		return err
	}
	if b.AsOf.IsZero() {
		return domain.ErrInvalidModel
	}
	// numeric(20,4) has at most 16 integer digits; never round an overflowing value.
	integer := strings.Split(strings.TrimPrefix(b.Value.Amount.String(), "-"), ".")[0]
	if len(integer) > 16 {
		return domain.ErrInvalidModel
	}
	return nil
}

func accountArgs(a domain.Account) []any {
	var balance any
	var asOf any
	if a.Balance != nil {
		balance = a.Balance.Value.Amount.String()
		asOf = a.Balance.AsOf
	}
	return []any{string(a.ID), string(a.UserID), string(a.BankID), string(a.ConnectionID),
		a.ExternalAccountID, a.Number, a.Name, accountTypes[a.Type], accountStatuses[a.Status], string(a.Currency), balance, asOf}
}

const insertAccount = `INSERT INTO accounts
 (id,user_id,bank_id,bank_connection_id,external_account_id,account_number,name,type,status,currency_code,reported_balance,balance_as_of)
 VALUES ($1,$2,$3,NULLIF($4,'')::uuid,NULLIF($5,''),NULLIF($6,''),$7,$8,$9,$10,$11::numeric,$12)`

func (r *AccountRepository) Create(ctx context.Context, a domain.Account) (domain.Account, error) {
	if err := validateAccount(a); err != nil {
		return domain.Account{}, err
	}
	return scanAccount(r.db.QueryRow(ctx, insertAccount+` RETURNING `+accountColumns, accountArgs(a)...))
}

func (r *AccountRepository) UpsertExternal(ctx context.Context, a domain.Account) (domain.Account, error) {
	if err := validateAccount(a); err != nil {
		return domain.Account{}, err
	}
	if a.ConnectionID == "" || a.ExternalAccountID == "" {
		return domain.Account{}, domain.ErrInvalidModel
	}
	result, err := scanAccount(r.db.QueryRow(ctx, insertAccount+`
 ON CONFLICT (bank_id,external_account_id) WHERE external_account_id IS NOT NULL
 DO UPDATE SET name=EXCLUDED.name, type=EXCLUDED.type, status=EXCLUDED.status,
 account_number=COALESCE(accounts.account_number,EXCLUDED.account_number),
 reported_balance=CASE WHEN EXCLUDED.balance_as_of IS NOT NULL AND
 (accounts.balance_as_of IS NULL OR EXCLUDED.balance_as_of >= accounts.balance_as_of)
 THEN EXCLUDED.reported_balance ELSE accounts.reported_balance END,
 balance_as_of=CASE WHEN EXCLUDED.balance_as_of IS NOT NULL AND
 (accounts.balance_as_of IS NULL OR EXCLUDED.balance_as_of >= accounts.balance_as_of)
 THEN EXCLUDED.balance_as_of ELSE accounts.balance_as_of END
 WHERE accounts.user_id=EXCLUDED.user_id AND accounts.bank_connection_id=EXCLUDED.bank_connection_id
 AND accounts.currency_code=EXCLUDED.currency_code
 AND (accounts.account_number IS NULL OR EXCLUDED.account_number IS NULL OR accounts.account_number=EXCLUDED.account_number)
 RETURNING `+accountColumns, accountArgs(a)...))
	if errors.Is(err, account.ErrNotFound) {
		return domain.Account{}, account.ErrConflict
	}
	return result, err
}

func (r *AccountRepository) Get(ctx context.Context, user domain.UserID, id domain.AccountID) (domain.Account, error) {
	if err := validIDs(string(user), string(id)); err != nil {
		return domain.Account{}, err
	}
	return scanAccount(r.db.QueryRow(ctx, `SELECT `+accountColumns+` FROM accounts WHERE user_id=$1 AND id=$2`, string(user), string(id)))
}

func (r *AccountRepository) ListByUser(ctx context.Context, user domain.UserID) ([]domain.Account, error) {
	if err := validIDs(string(user)); err != nil {
		return nil, err
	}
	rows, err := r.db.Query(ctx, `SELECT `+accountColumns+` FROM accounts WHERE user_id=$1 ORDER BY id`, string(user))
	if err != nil {
		return nil, repositoryError(err, account.ErrNotFound, account.ErrConflict)
	}
	defer rows.Close()
	result := make([]domain.Account, 0)
	for rows.Next() {
		a, err := scanAccount(rows)
		if err != nil {
			return nil, err
		}
		result = append(result, a)
	}
	if err := rows.Err(); err != nil {
		return nil, repositoryError(err, account.ErrNotFound, account.ErrConflict)
	}
	return result, nil
}

func (r *AccountRepository) BindExternal(ctx context.Context, user domain.UserID, id domain.AccountID, connection domain.BankConnectionID, external string) (domain.Account, error) {
	if err := validIDs(string(user), string(id), string(connection)); err != nil {
		return domain.Account{}, err
	}
	if external == "" || external != strings.TrimSpace(external) {
		return domain.Account{}, domain.ErrInvalidModel
	}
	a, err := scanAccount(r.db.QueryRow(ctx, `UPDATE accounts SET bank_connection_id=$3,external_account_id=$4
 WHERE user_id=$1 AND id=$2
 AND (bank_connection_id IS NULL OR bank_connection_id=$3)
 AND (external_account_id IS NULL OR external_account_id=$4)
 AND EXISTS (SELECT 1 FROM bank_connections c WHERE c.id=$3 AND c.user_id=accounts.user_id AND c.bank_id=accounts.bank_id)
 RETURNING `+accountColumns, string(user), string(id), string(connection), external))
	if errors.Is(err, account.ErrNotFound) {
		if _, getErr := r.Get(ctx, user, id); getErr != nil {
			return domain.Account{}, getErr
		}
		return domain.Account{}, account.ErrConflict
	}
	return a, err
}

func (r *AccountRepository) UpdateBalance(ctx context.Context, user domain.UserID, id domain.AccountID, b domain.BalanceSnapshot) error {
	if err := validIDs(string(user), string(id)); err != nil {
		return err
	}
	if err := validateBalance(b); err != nil {
		return err
	}
	tag, err := r.db.Exec(ctx, `UPDATE accounts SET reported_balance=$3::numeric,balance_as_of=$4
 WHERE user_id=$1 AND id=$2 AND currency_code=$5 AND (balance_as_of IS NULL OR balance_as_of <= $4)`,
		string(user), string(id), b.Value.Amount.String(), b.AsOf, string(b.Value.Currency))
	if err != nil {
		return repositoryError(err, account.ErrNotFound, account.ErrConflict)
	}
	if tag.RowsAffected() == 0 {
		a, err := r.Get(ctx, user, id)
		if err != nil {
			return err
		}
		if a.Currency != b.Value.Currency {
			return money.ErrCurrencyMismatch
		}
	}
	return nil
}
