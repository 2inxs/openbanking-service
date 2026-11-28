package transaction

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/2inxs/openbanking-service/internal/domain"
	"github.com/2inxs/openbanking-service/internal/domain/money"
)

var (
	ErrAccountNotFound = domain.ErrAccountNotFound
	ErrInvalidInput    = domain.ErrInvalidModel
)

const (
	DefaultLimit   = 50
	MaxLimit       = 200
	maxStrictIDLen = 128
)

// AccountReader проверяет существование и принадлежность счёта пользователю.
type AccountReader interface {
	Get(ctx context.Context, userID domain.UserID, id domain.AccountID) (domain.Account, error)
}

// ListQuery описывает параметры выборки операций по счёту за период с курсорной пагинацией.
type ListQuery struct {
	UserID    domain.UserID
	AccountID domain.AccountID
	From      *time.Time // Включительно (>= From)
	To        *time.Time // Исключительно (< To)
	After     *Cursor
	Limit     int
}

// TransactionItem представляет операцию по счёту на уровне сценариев (без транспортных тегов).
type TransactionItem struct {
	ID                    domain.TransactionID
	AccountID             domain.AccountID
	StatementImportID     domain.StatementImportID
	ExternalTransactionID string
	Source                domain.TransactionSourceID
	Status                domain.TransactionStatusID
	Amount                money.Amount
	Currency              money.Currency
	OriginalAmount        *money.Amount
	OriginalCurrency      *money.Currency
	OccurredAt            time.Time
	BookedAt              *time.Time
	ValueAt               *time.Time
	Description           string
	CounterpartyName      string
	CounterpartyAccount   string
	MerchantName          string
	MCC                   string
}

// ListResult возвращает страницу операций и курсор для следующей страницы (nil, если записей больше нет).
type ListResult struct {
	Items []TransactionItem
	Next  *Cursor
}

type Service struct {
	accounts AccountReader
	repo     Repository
}

func NewService(accounts AccountReader, repo Repository) *Service {
	return &Service{
		accounts: accounts,
		repo:     repo,
	}
}

func validateStrictID(raw string) error {
	if raw == "" || len(raw) > maxStrictIDLen || raw != strings.TrimSpace(raw) {
		return ErrInvalidInput
	}
	for i := 0; i < len(raw); i++ {
		if raw[i] <= 0x20 || raw[i] == 0x7f {
			return ErrInvalidInput
		}
	}
	return nil
}

func validatePeriod(from, to *time.Time) (*time.Time, *time.Time, error) {
	var normFrom, normTo *time.Time
	if from != nil {
		if from.IsZero() {
			return nil, nil, fmt.Errorf("%w: from timestamp must not be zero", ErrInvalidInput)
		}
		f := from.UTC()
		normFrom = &f
	}
	if to != nil {
		if to.IsZero() {
			return nil, nil, fmt.Errorf("%w: to timestamp must not be zero", ErrInvalidInput)
		}
		t := to.UTC()
		normTo = &t
	}
	if normFrom != nil && normTo != nil && !normFrom.Before(*normTo) {
		return nil, nil, fmt.Errorf("%w: from must be strictly before to", ErrInvalidInput)
	}
	return normFrom, normTo, nil
}

func validateCursor(after *Cursor) (*Cursor, error) {
	if after == nil {
		return nil, nil
	}
	if after.OccurredAt.IsZero() {
		return nil, fmt.Errorf("%w: cursor occurred_at must not be zero", ErrInvalidInput)
	}
	if err := validateStrictID(string(after.ID)); err != nil {
		return nil, fmt.Errorf("%w: invalid cursor id", ErrInvalidInput)
	}
	return &Cursor{
		OccurredAt: after.OccurredAt.UTC(),
		ID:         after.ID,
	}, nil
}

func normalizeLimit(limit int) (int, error) {
	if limit < 0 || limit > MaxLimit {
		return 0, fmt.Errorf("%w: limit must be between 1 and %d", ErrInvalidInput, MaxLimit)
	}
	if limit == 0 {
		return DefaultLimit, nil
	}
	return limit, nil
}

func mapRecord(rec Record, acc domain.Account) (TransactionItem, error) {
	if err := rec.Transaction.ValidateForAccount(acc); err != nil {
		return TransactionItem{}, fmt.Errorf("%w: %v", ErrInvalidInput, err)
	}
	if rec.OccurredAt.IsZero() {
		return TransactionItem{}, fmt.Errorf("%w: record occurred_at must not be zero", ErrInvalidInput)
	}

	item := TransactionItem{
		ID:                    rec.Transaction.ID,
		AccountID:             rec.Transaction.AccountID,
		StatementImportID:     rec.Transaction.StatementImportID,
		ExternalTransactionID: rec.Transaction.ExternalTransactionID,
		Source:                rec.Transaction.Source,
		Status:                rec.Transaction.Status,
		Amount:                rec.Transaction.Value.Amount,
		Currency:              rec.Transaction.Value.Currency,
		OccurredAt:            rec.OccurredAt.UTC(),
		Description:           rec.Transaction.Description,
		CounterpartyName:      rec.Transaction.CounterpartyName,
		CounterpartyAccount:   rec.Transaction.CounterpartyAccount,
		MerchantName:          rec.Transaction.MerchantName,
		MCC:                   rec.Transaction.MCC,
	}

	if rec.Transaction.OriginalValue != nil {
		amt := rec.Transaction.OriginalValue.Amount
		cur := rec.Transaction.OriginalValue.Currency
		item.OriginalAmount = &amt
		item.OriginalCurrency = &cur
	}
	if rec.Transaction.BookedAt != nil {
		t := rec.Transaction.BookedAt.UTC()
		item.BookedAt = &t
	}
	if rec.Transaction.ValueAt != nil {
		t := rec.Transaction.ValueAt.UTC()
		item.ValueAt = &t
	}

	return item, nil
}

// ListAccountTransactions возвращает страницу операций по счёту пользователя за период [From, To).
// Стабильная сортировка: OccurredAt DESC, ID DESC — записи с одинаковым временем не теряются при пагинации.
func (s *Service) ListAccountTransactions(ctx context.Context, q ListQuery) (ListResult, error) {
	if s == nil || s.accounts == nil || s.repo == nil {
		return ListResult{}, ErrInvalidInput
	}
	if err := validateStrictID(string(q.UserID)); err != nil {
		return ListResult{}, err
	}
	if err := validateStrictID(string(q.AccountID)); err != nil {
		return ListResult{}, err
	}
	from, to, err := validatePeriod(q.From, q.To)
	if err != nil {
		return ListResult{}, err
	}
	after, err := validateCursor(q.After)
	if err != nil {
		return ListResult{}, err
	}
	limit, err := normalizeLimit(q.Limit)
	if err != nil {
		return ListResult{}, err
	}

	acc, err := s.accounts.Get(ctx, q.UserID, q.AccountID)
	if err != nil {
		if errors.Is(err, domain.ErrAccountNotFound) {
			return ListResult{}, ErrAccountNotFound
		}
		return ListResult{}, err
	}
	if acc.UserID != q.UserID || acc.ID != q.AccountID {
		return ListResult{}, ErrAccountNotFound
	}

	page, err := s.repo.List(ctx, q.UserID, Filter{
		AccountID: q.AccountID,
		From:      from,
		To:        to,
		After:     after,
		Limit:     limit,
	})
	if err != nil {
		if errors.Is(err, ErrNotFound) || errors.Is(err, domain.ErrAccountNotFound) {
			return ListResult{}, ErrAccountNotFound
		}
		return ListResult{}, err
	}

	items := make([]TransactionItem, 0, len(page.Items))
	for _, rec := range page.Items {
		item, err := mapRecord(rec, acc)
		if err != nil {
			return ListResult{}, err
		}
		items = append(items, item)
	}

	sort.SliceStable(items, func(i, j int) bool {
		if !items[i].OccurredAt.Equal(items[j].OccurredAt) {
			return items[i].OccurredAt.After(items[j].OccurredAt)
		}
		return items[i].ID > items[j].ID
	})

	var next *Cursor
	if page.Next != nil {
		if _, err := validateCursor(page.Next); err != nil {
			return ListResult{}, err
		}
		next = &Cursor{
			OccurredAt: page.Next.OccurredAt.UTC(),
			ID:         page.Next.ID,
		}
	}

	return ListResult{
		Items: items,
		Next:  next,
	}, nil
}
