package statement

import (
	"errors"
	"fmt"
	"io"
	"time"

	"github.com/2inxs/openbanking-service/internal/domain/money"
)

const MaxRows = 100_000

var (
	ErrEmptyStatement   = errors.New("statement: file has no transactions")
	ErrInvalidHeader    = errors.New("statement: invalid statement header")
	ErrTooManyRows      = errors.New("statement: row limit exceeded")
	ErrMalformedFile    = errors.New("statement: malformed file structure")
	ErrValidationFailed = errors.New("statement: one or more rows are invalid")
)

type RowError struct {
	Row     int    `json:"row"`
	Message string `json:"message"`
}

func (e RowError) Error() string {
	return fmt.Sprintf("row %d: %s", e.Row, e.Message)
}

type TransactionRow struct {
	RowNumber        int
	ExternalID       string
	BookedAt         time.Time
	Value            money.Money
	Description      string
	CounterpartyName string
}

type ParsedStatement struct {
	AccountNumber string
	Currency      money.Currency
	Transactions  []TransactionRow
	RowErrors     []RowError
}

type Parser interface {
	Parse(r io.Reader) (ParsedStatement, error)
}
