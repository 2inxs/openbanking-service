package statement

import (
	"bufio"
	"bytes"
	"encoding/csv"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/2inxs/openbanking-service/internal/domain/money"
)

const (
	CSVDelimiter        = ';'
	CSVDecimalSeparator = '.'
	CSVEncoding         = "utf-8"
)

var expectedCSVHeader = []string{
	"account_number",
	"booked_at",
	"amount",
	"currency",
	"external_id",
	"description",
	"counterparty",
}

// CSVParser разбирает банковскую выписку в строго зафиксированном CSV-формате.
type CSVParser struct {
	maxRows int
}

func NewCSVParser() *CSVParser {
	return &CSVParser{maxRows: MaxRows}
}

// Parse читает CSV из r, проверяет заголовок и каждую строку.
// При наличии ошибок в строках возвращает частично заполненный Statement (с заполненным RowErrors)
// и ошибку, оборачивающую ErrValidationFailed.
func (p *CSVParser) Parse(r io.Reader) (Statement, error) {
	if r == nil {
		return Statement{}, ErrEmptyStatement
	}

	br := bufio.NewReader(r)
	if err := stripUTF8BOM(br); err != nil {
		return Statement{}, fmt.Errorf("%w: %v", ErrMalformedCSV, err)
	}

	reader := csv.NewReader(br)
	reader.Comma = CSVDelimiter
	reader.FieldsPerRecord = len(expectedCSVHeader)
	reader.ReuseRecord = false

	header, err := reader.Read()
	if err != nil {
		if errors.Is(err, io.EOF) {
			return Statement{}, ErrEmptyStatement
		}
		return Statement{}, fmt.Errorf("%w: %v", ErrMalformedCSV, err)
	}
	if err := validateHeader(header); err != nil {
		return Statement{}, err
	}

	var stmt Statement
	rowNumber := 1 // 1 — строка заголовка; операции начинаются со 2-й строки

	for {
		record, err := reader.Read()
		if errors.Is(err, io.EOF) {
			break
		}
		rowNumber++

		if err != nil {
			var parseErr *csv.ParseError
			if errors.As(err, &parseErr) {
				return Statement{}, fmt.Errorf("%w at row %d: %v", ErrMalformedCSV, rowNumber, parseErr.Err)
			}
			return Statement{}, fmt.Errorf("%w at row %d: %v", ErrMalformedCSV, rowNumber, err)
		}

		if len(stmt.Transactions)+len(stmt.RowErrors) >= p.maxRows {
			return Statement{}, fmt.Errorf("%w: max %d rows allowed", ErrTooManyRows, p.maxRows)
		}

		accNum := strings.TrimSpace(record[0])
		if accNum == "" {
			stmt.RowErrors = append(stmt.RowErrors, RowError{
				Row:     rowNumber,
				Message: "account_number is required",
			})
			continue
		}
		if stmt.AccountNumber == "" {
			stmt.AccountNumber = accNum
		} else if accNum != stmt.AccountNumber {
			stmt.RowErrors = append(stmt.RowErrors, RowError{
				Row:     rowNumber,
				Message: fmt.Sprintf("account_number %q does not match statement account %q", accNum, stmt.AccountNumber),
			})
			continue
		}

		bookedAt, err := parseStatementDate(record[1])
		if err != nil {
			stmt.RowErrors = append(stmt.RowErrors, RowError{
				Row:     rowNumber,
				Message: err.Error(),
			})
			continue
		}

		curr := money.Currency(strings.TrimSpace(record[3]))
		if err := curr.Validate(); err != nil {
			stmt.RowErrors = append(stmt.RowErrors, RowError{
				Row:     rowNumber,
				Message: fmt.Sprintf("invalid currency %q: %v", record[3], err),
			})
			continue
		}
		if stmt.Currency == "" {
			stmt.Currency = curr
		} else if curr != stmt.Currency {
			stmt.RowErrors = append(stmt.RowErrors, RowError{
				Row:     rowNumber,
				Message: fmt.Sprintf("currency %q does not match statement currency %q", curr, stmt.Currency),
			})
			continue
		}

		amount, err := money.Parse(strings.TrimSpace(record[2]))
		if err != nil {
			stmt.RowErrors = append(stmt.RowErrors, RowError{
				Row:     rowNumber,
				Message: fmt.Sprintf("invalid amount %q: %v", record[2], err),
			})
			continue
		}
		if amount.Sign() == 0 {
			stmt.RowErrors = append(stmt.RowErrors, RowError{
				Row:     rowNumber,
				Message: "amount must be non-zero",
			})
			continue
		}

		val, err := money.New(amount, curr)
		if err != nil {
			stmt.RowErrors = append(stmt.RowErrors, RowError{
				Row:     rowNumber,
				Message: err.Error(),
			})
			continue
		}

		extID := strings.TrimSpace(record[4])
		if strings.ContainsAny(extID, "\r\n\t") {
			stmt.RowErrors = append(stmt.RowErrors, RowError{
				Row:     rowNumber,
				Message: "external_id must not contain control characters",
			})
			continue
		}

		stmt.Transactions = append(stmt.Transactions, TransactionRow{
			RowNumber:        rowNumber,
			ExternalID:       extID,
			BookedAt:         bookedAt,
			Value:            val,
			Description:      strings.TrimSpace(record[5]),
			CounterpartyName: strings.TrimSpace(record[6]),
		})
	}

	if len(stmt.Transactions) == 0 && len(stmt.RowErrors) == 0 {
		return Statement{}, ErrEmptyStatement
	}

	if len(stmt.RowErrors) > 0 {
		return stmt, fmt.Errorf("%w: %d invalid rows", ErrValidationFailed, len(stmt.RowErrors))
	}

	return stmt, nil
}

func stripUTF8BOM(br *bufio.Reader) error {
	bom := []byte{0xEF, 0xBB, 0xBF}
	peeked, err := br.Peek(len(bom))
	if err != nil && !errors.Is(err, io.EOF) && !errors.Is(err, bufio.ErrBufferFull) {
		return err
	}
	if bytes.Equal(peeked, bom) {
		_, err = br.Discard(len(bom))
		return err
	}
	return nil
}

func validateHeader(got []string) error {
	if len(got) != len(expectedCSVHeader) {
		return fmt.Errorf("%w: expected %d columns, got %d", ErrInvalidHeader, len(expectedCSVHeader), len(got))
	}
	for i, want := range expectedCSVHeader {
		if strings.TrimSpace(got[i]) != want {
			return fmt.Errorf("%w: column %d must be %q, got %q", ErrInvalidHeader, i+1, want, got[i])
		}
	}
	return nil
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
