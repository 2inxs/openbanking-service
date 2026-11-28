package postgres

import (
	"context"
	"errors"
	"github.com/2inxs/openbanking-service/internal/domain"
	"github.com/2inxs/openbanking-service/internal/domain/money"
	tr "github.com/2inxs/openbanking-service/internal/usecase/transaction"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"regexp"
	"strings"
	"time"
)

type TransactionRepository struct{ db DBTX }

var _ tr.Repository = (*TransactionRepository)(nil)

func NewTransactionRepository(db DBTX) *TransactionRepository { return &TransactionRepository{db: db} }

var hashPattern = regexp.MustCompile(`^[0-9a-f]{64}$`)
var txStatuses = map[domain.TransactionStatusID]string{domain.TransactionPending: "pending", domain.TransactionBooked: "booked", domain.TransactionRejected: "rejected"}
var txSources = map[domain.TransactionSourceID]string{domain.SourceOpenBanking: "open_banking", domain.SourceStatement: "statement"}

const transactionColumns = `t.id::text,t.account_id::text,COALESCE(t.statement_import_id::text,''),COALESCE(t.external_transaction_id,''),t.source,t.status,
 t.amount::text,t.currency_code,t.original_amount::text,t.original_currency_code,t.booked_at,t.value_at,COALESCE(t.description,''),COALESCE(t.counterparty_name,''),
 COALESCE(t.counterparty_account,''),COALESCE(t.merchant_name,''),COALESCE(t.mcc,''),t.occurred_at,t.deduplication_key,COALESCE(t.raw_bank_status,'')`

func transactionError(err error) error {
	var pe *pgconn.PgError
	if errors.As(err, &pe) && pe.Code == "23505" && (pe.ConstraintName == "transactions_external_uq" || pe.ConstraintName == "transactions_dedup_uq") {
		return tr.ErrConflict
	}
	return repositoryError(err, tr.ErrNotFound, domain.ErrInvalidModel)
}
func scanTransaction(row pgx.Row) (tr.Record, error) {
	var r tr.Record
	t := &r.Transaction
	var source, status, amount string
	var original, currency *string
	if err := row.Scan(&t.ID, &t.AccountID, &t.StatementImportID, &t.ExternalTransactionID, &source, &status, &amount, &t.Value.Currency, &original, &currency, &t.BookedAt, &t.ValueAt,
		&t.Description, &t.CounterpartyName, &t.CounterpartyAccount, &t.MerchantName, &t.MCC, &r.OccurredAt, &r.DeduplicationKey, &r.RawBankStatus); err != nil {
		return r, transactionError(err)
	}
	for id, s := range txStatuses {
		if s == status {
			t.Status = id
		}
	}
	for id, s := range txSources {
		if s == source {
			t.Source = id
		}
	}
	var err error
	t.Value.Amount, err = money.Parse(amount)
	if err != nil {
		return tr.Record{}, ErrUnavailable
	}
	if original != nil {
		if currency == nil {
			return tr.Record{}, ErrUnavailable
		}
		a, e := money.Parse(*original)
		if e != nil {
			return tr.Record{}, ErrUnavailable
		}
		t.OriginalValue = &money.Money{Amount: a, Currency: money.Currency(*currency)}
	}
	if err := t.Validate(); err != nil {
		return tr.Record{}, ErrUnavailable
	}
	return r, nil
}
func (r *TransactionRepository) Create(ctx context.Context, user domain.UserID, record tr.Record) (tr.Record, error) {
	t := record.Transaction
	if err := t.Validate(); err != nil {
		return tr.Record{}, err
	}
	if err := validIDs(string(user), string(t.ID), string(t.AccountID)); err != nil {
		return tr.Record{}, err
	}
	if t.StatementImportID != "" {
		if err := validIDs(string(t.StatementImportID)); err != nil {
			return tr.Record{}, err
		}
	}
	if record.OccurredAt.IsZero() || !hashPattern.MatchString(record.DeduplicationKey) || (t.BookedAt == nil && t.ValueAt == nil) {
		return tr.Record{}, domain.ErrInvalidModel
	}
	for _, m := range []*money.Money{&t.Value, t.OriginalValue} {
		if m != nil && len(strings.Split(strings.TrimPrefix(m.Amount.String(), "-"), ".")[0]) > 16 {
			return tr.Record{}, domain.ErrInvalidModel
		}
	}
	var original, originalCurrency any
	if t.OriginalValue != nil {
		original = t.OriginalValue.Amount.String()
		originalCurrency = string(t.OriginalValue.Currency)
	}
	return scanTransaction(r.db.QueryRow(ctx, `INSERT INTO transactions AS t
 (id,account_id,statement_import_id,external_transaction_id,source,status,amount,currency_code,original_amount,original_currency_code,booked_at,value_at,description,counterparty_name,counterparty_account,merchant_name,mcc,occurred_at,deduplication_key,raw_bank_status)
 SELECT $1,$2,NULLIF($3,'')::uuid,NULLIF($4,''),$5,$6,$7::numeric,$8,$9::numeric,$10,$11,$12,NULLIF($13,''),NULLIF($14,''),NULLIF($15,''),NULLIF($16,''),NULLIF($17,''),$18,$19,NULLIF($20,'')
 FROM accounts a WHERE a.id=$2 AND a.user_id=$21 RETURNING `+transactionColumns,
		string(t.ID), string(t.AccountID), string(t.StatementImportID), t.ExternalTransactionID, txSources[t.Source], txStatuses[t.Status], t.Value.Amount.String(), string(t.Value.Currency), original, originalCurrency,
		t.BookedAt, t.ValueAt, t.Description, t.CounterpartyName, t.CounterpartyAccount, t.MerchantName, t.MCC, record.OccurredAt, record.DeduplicationKey, record.RawBankStatus, string(user)))
}
func (r *TransactionRepository) find(ctx context.Context, user domain.UserID, acc domain.AccountID, value string, external bool) (tr.Record, error) {
	if err := validIDs(string(user), string(acc)); err != nil {
		return tr.Record{}, err
	}
	if strings.TrimSpace(value) == "" {
		return tr.Record{}, domain.ErrInvalidModel
	}
	column := "deduplication_key"
	if external {
		column = "external_transaction_id"
	} else if !hashPattern.MatchString(value) {
		return tr.Record{}, domain.ErrInvalidModel
	}
	return scanTransaction(r.db.QueryRow(ctx, `SELECT `+transactionColumns+` FROM transactions t JOIN accounts a ON a.id=t.account_id WHERE a.user_id=$1 AND t.account_id=$2 AND t.`+column+`=$3`, string(user), string(acc), value))
}
func (r *TransactionRepository) GetByExternalID(ctx context.Context, u domain.UserID, a domain.AccountID, id string) (tr.Record, error) {
	return r.find(ctx, u, a, id, true)
}
func (r *TransactionRepository) GetByDeduplicationKey(ctx context.Context, u domain.UserID, a domain.AccountID, key string) (tr.Record, error) {
	return r.find(ctx, u, a, key, false)
}
func (r *TransactionRepository) List(ctx context.Context, user domain.UserID, f tr.Filter) (tr.Page, error) {
	if err := validIDs(string(user), string(f.AccountID)); err != nil {
		return tr.Page{}, err
	}
	if f.Limit == 0 {
		f.Limit = 50
	}
	if f.Limit < 1 || f.Limit > 200 {
		return tr.Page{}, domain.ErrInvalidModel
	}
	if (f.From != nil && f.From.IsZero()) || (f.To != nil && f.To.IsZero()) || (f.From != nil && f.To != nil && !f.From.Before(*f.To)) {
		return tr.Page{}, domain.ErrInvalidModel
	}
	var afterTime *time.Time
	var afterID any
	if f.After != nil {
		if f.After.OccurredAt.IsZero() {
			return tr.Page{}, domain.ErrInvalidModel
		}
		if err := validIDs(string(f.After.ID)); err != nil {
			return tr.Page{}, err
		}
		afterTime = &f.After.OccurredAt
		afterID = string(f.After.ID)
	}
	var accessible bool
	if err := r.db.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM accounts WHERE id=$1 AND user_id=$2)`, string(f.AccountID), string(user)).Scan(&accessible); err != nil {
		return tr.Page{}, transactionError(err)
	}
	if !accessible {
		return tr.Page{}, tr.ErrNotFound
	}
	rows, err := r.db.Query(ctx, `SELECT `+transactionColumns+` FROM transactions t JOIN accounts a ON a.id=t.account_id
 WHERE a.user_id=$1 AND t.account_id=$2 AND ($3::timestamptz IS NULL OR t.occurred_at >= $3) AND ($4::timestamptz IS NULL OR t.occurred_at < $4)
 AND ($5::timestamptz IS NULL OR (t.occurred_at,t.id)<($5,$6::uuid)) ORDER BY t.occurred_at DESC,t.id DESC LIMIT $7`, string(user), string(f.AccountID), f.From, f.To, afterTime, afterID, f.Limit+1)
	if err != nil {
		return tr.Page{}, transactionError(err)
	}
	defer rows.Close()
	page := tr.Page{Items: make([]tr.Record, 0, f.Limit)}
	for rows.Next() {
		v, e := scanTransaction(rows)
		if e != nil {
			return tr.Page{}, e
		}
		page.Items = append(page.Items, v)
	}
	if err := rows.Err(); err != nil {
		return tr.Page{}, transactionError(err)
	}
	if len(page.Items) > f.Limit {
		page.Items = page.Items[:f.Limit]
		last := page.Items[len(page.Items)-1]
		page.Next = &tr.Cursor{OccurredAt: last.OccurredAt, ID: last.Transaction.ID}
	}
	return page, nil
}
