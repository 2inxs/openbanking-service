package statement

import (
	"errors"
	"fmt"
	"github.com/2inxs/openbanking-service/internal/domain/money"
	usecasestmt "github.com/2inxs/openbanking-service/internal/usecase/statement"
	"strings"
	"time"
)

var expectedStatementHeader = []string{
	"account_number",
	"booked_at",
	"amount",
	"currency",
	"external_id",
	"description",
	"counterparty",
}

func validateHeader(got []string) error {
	if len(got) != len(expectedStatementHeader) {
		return fmt.Errorf("%w: expected %d columns, got %d", usecasestmt.ErrInvalidHeader, len(expectedStatementHeader), len(got))
	}
	for i, want := range expectedStatementHeader {
		if strings.TrimSpace(got[i]) != want {
			return fmt.Errorf("%w: column %d must be %q, got %q", usecasestmt.ErrInvalidHeader, i+1, want, got[i])
		}
	}
	return nil
}

// normalizeRow проверяет и преобразует 7 ячеек строки в нормализованную операцию.
func normalizeRow(
	rowNumber int,
	cells []string,
	stmtAccount *string,
	stmtCurrency *money.Currency,
	parseDate func(raw string) (time.Time, error),
) (usecasestmt.TransactionRow, *usecasestmt.RowError) {
	if len(cells) != len(expectedStatementHeader) {
		return usecasestmt.TransactionRow{}, &usecasestmt.RowError{
			Row:     rowNumber,
			Message: fmt.Sprintf("expected %d columns, got %d", len(expectedStatementHeader), len(cells)),
		}
	}

	accNum := strings.TrimSpace(cells[0])
	if accNum == "" {
		return usecasestmt.TransactionRow{}, &usecasestmt.RowError{
			Row:     rowNumber,
			Message: "account_number is required",
		}
	}
	if *stmtAccount == "" {
		*stmtAccount = accNum
	} else if accNum != *stmtAccount {
		return usecasestmt.TransactionRow{}, &usecasestmt.RowError{
			Row:     rowNumber,
			Message: fmt.Sprintf("account_number %q does not match statement account %q", accNum, *stmtAccount),
		}
	}

	if parseDate == nil {
		parseDate = parseStatementDate
	}
	bookedAt, err := parseDate(cells[1])
	if err != nil {
		return usecasestmt.TransactionRow{}, &usecasestmt.RowError{
			Row:     rowNumber,
			Message: err.Error(),
		}
	}

	curr := money.Currency(strings.TrimSpace(cells[3]))
	if err := curr.Validate(); err != nil {
		return usecasestmt.TransactionRow{}, &usecasestmt.RowError{
			Row:     rowNumber,
			Message: fmt.Sprintf("invalid currency %q: %v", cells[3], err),
		}
	}
	if *stmtCurrency == "" {
		*stmtCurrency = curr
	} else if curr != *stmtCurrency {
		return usecasestmt.TransactionRow{}, &usecasestmt.RowError{
			Row:     rowNumber,
			Message: fmt.Sprintf("currency %q does not match statement currency %q", curr, *stmtCurrency),
		}
	}

	amount, err := money.Parse(strings.TrimSpace(cells[2]))
	if err != nil {
		return usecasestmt.TransactionRow{}, &usecasestmt.RowError{
			Row:     rowNumber,
			Message: fmt.Sprintf("invalid amount %q: %v", cells[2], err),
		}
	}
	if amount.Sign() == 0 {
		return usecasestmt.TransactionRow{}, &usecasestmt.RowError{
			Row:     rowNumber,
			Message: "amount must be non-zero",
		}
	}

	val, err := money.New(amount, curr)
	if err != nil {
		return usecasestmt.TransactionRow{}, &usecasestmt.RowError{
			Row:     rowNumber,
			Message: err.Error(),
		}
	}

	extID := strings.TrimSpace(cells[4])
	if strings.ContainsAny(extID, "\r\n\t") {
		return usecasestmt.TransactionRow{}, &usecasestmt.RowError{
			Row:     rowNumber,
			Message: "external_id must not contain control characters",
		}
	}

	return usecasestmt.TransactionRow{
		RowNumber:        rowNumber,
		ExternalID:       extID,
		BookedAt:         bookedAt,
		Value:            val,
		Description:      strings.TrimSpace(cells[5]),
		CounterpartyName: strings.TrimSpace(cells[6]),
	}, nil
}

func parseStatementDate(raw string) (time.Time, error) {
	s := strings.TrimSpace(raw)
	if s == "" {
		return time.Time{}, errors.New("booked_at is required")
	}
	if t, err := time.Parse("2006-01-02", s); err == nil {
		return t.UTC(), nil
	}
	if t, err := time.Parse(time.RFC3339, s); err == nil {
		return t.UTC(), nil
	}
	return time.Time{}, fmt.Errorf("invalid booked_at %q: expected YYYY-MM-DD or RFC3339", raw)
}
