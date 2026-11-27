package transaction

import (
	"context"
	"errors"
	"github.com/2inxs/openbanking-service/internal/domain"
	"time"
)

var (
	ErrNotFound = errors.New("transaction: not found")
	ErrConflict = errors.New("transaction: duplicate identity")
)

type Record struct {
	Transaction      domain.Transaction
	OccurredAt       time.Time
	DeduplicationKey string
	RawBankStatus    string
}

type Cursor struct {
	OccurredAt time.Time
	ID         domain.TransactionID
}
type Filter struct {
	AccountID domain.AccountID
	From, To  *time.Time // Inclusive from, exclusive to; filters use occurred_at.
	After     *Cursor
	Limit     int
}
type Page struct {
	Items []Record
	Next  *Cursor
}

type Repository interface {
	Create(context.Context, domain.UserID, Record) (Record, error)
	GetByExternalID(context.Context, domain.UserID, domain.AccountID, string) (Record, error)
	GetByDeduplicationKey(context.Context, domain.UserID, domain.AccountID, string) (Record, error)
	List(context.Context, domain.UserID, Filter) (Page, error)
}
