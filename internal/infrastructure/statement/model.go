package statement

import (
	"errors"
	"fmt"
	"time"

	"github.com/2inxs/openbanking-service/internal/domain/money"
)

const MaxRows = 100_000

var (
	ErrEmptyStatement   = errors.New("statement: file has no transactions")
	ErrInvalidHeader    = errors.New("statement: invalid CSV header")
	ErrTooManyRows      = errors.New("statement: row limit exceeded")
	ErrMalformedCSV     = errors.New("statement: malformed CSV structure")
	ErrValidationFailed = errors.New("statement: one or more rows are invalid")
)

// RowError описывает ошибку разбора конкретной строки выписки (нумерация с 1, включая заголовок).
type RowError struct {
	Row     int    `json:"row"`
	Message string `json:"message"`
}

func (e RowError) Error() string {
	return fmt.Sprintf("row %d: %s", e.Row, e.Message)
}

// TransactionRow представляет нормализованную операцию из выписки (общая модель для CSV и XLSX).
type TransactionRow struct {
	RowNumber        int
	ExternalID       string
	BookedAt         time.Time
	Value            money.Money
	Description      string
	CounterpartyName string
}

// Statement содержит разобранные реквизиты выписки и список операций.
type Statement struct {
	AccountNumber string
	Currency      money.Currency
	Transactions  []TransactionRow
	RowErrors     []RowError
}
