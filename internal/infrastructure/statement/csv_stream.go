package statement

import (
	"bufio"
	"context"
	"encoding/csv"
	"errors"
	"fmt"
	"io"

	"github.com/2inxs/openbanking-service/internal/domain/money"
	usecasestmt "github.com/2inxs/openbanking-service/internal/usecase/statement"
)

const streamReadBufferSize = 64 << 10 // 64 КБ

var _ usecasestmt.StreamParser = (*CSVParser)(nil)

func (p *CSVParser) WithMaxFileSize(maxBytes int64) *CSVParser {
	if maxBytes > 0 {
		p.maxFileSize = maxBytes
	}
	return p
}

func (p *CSVParser) WithMaxRows(maxRows int) *CSVParser {
	if maxRows > 0 {
		p.maxRows = maxRows
	}
	return p
}

// ParseStream читает CSV-поток буферами по 64 КБ, валидирует строки на лету
// и отправляет готовые чанки в канал chunks для параллельной обработки/записи в БД.
// Канал chunks закрывается автоматически при завершении метода.
func (p *CSVParser) ParseStream(ctx context.Context, r io.Reader, chunks chan<- usecasestmt.TransactionChunk) ([]usecasestmt.RowError, error) {
	defer close(chunks)

	if r == nil {
		return nil, usecasestmt.ErrEmptyStatement
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	limited := &limitedReader{r: r, remaining: p.maxFileSize}
	br := bufio.NewReaderSize(limited, streamReadBufferSize)
	if err := stripUTF8BOM(br); err != nil {
		if errors.Is(err, usecasestmt.ErrFileTooLarge) {
			return nil, err
		}
		return nil, fmt.Errorf("%w: %v", usecasestmt.ErrMalformedFile, err)
	}

	reader := csv.NewReader(br)
	reader.Comma = CSVDelimiter
	reader.FieldsPerRecord = len(expectedStatementHeader)
	reader.ReuseRecord = false

	header, err := reader.Read()
	if err != nil {
		if limited.exceeded || errors.Is(err, usecasestmt.ErrFileTooLarge) {
			return nil, usecasestmt.ErrFileTooLarge
		}
		if errors.Is(err, io.EOF) {
			return nil, usecasestmt.ErrEmptyStatement
		}
		return nil, fmt.Errorf("%w: %v", usecasestmt.ErrMalformedFile, err)
	}
	for i, col := range header {
		if !isValidUTF8String(col) {
			return nil, fmt.Errorf("%w: column %d contains invalid UTF-8", usecasestmt.ErrInvalidHeader, i+1)
		}
	}
	if err := validateHeader(header); err != nil {
		return nil, err
	}

	var (
		accountNumber string
		currency      money.Currency
		totalRows     int
		validRows     int
		rowErrors     []usecasestmt.RowError
		batch         = make([]usecasestmt.TransactionRow, 0, usecasestmt.DefaultChunkSize)
	)

	flushBatch := func() error {
		if len(batch) == 0 {
			return nil
		}
		chunk := usecasestmt.TransactionChunk{
			AccountNumber: accountNumber,
			Currency:      currency,
			Rows:          batch,
		}
		select {
		case chunks <- chunk:
			batch = make([]usecasestmt.TransactionRow, 0, usecasestmt.DefaultChunkSize)
			return nil
		case <-ctx.Done():
			return ctx.Err()
		}
	}

	for {
		if err := ctx.Err(); err != nil {
			return rowErrors, err
		}

		record, err := reader.Read()
		if limited.exceeded || errors.Is(err, usecasestmt.ErrFileTooLarge) {
			return rowErrors, usecasestmt.ErrFileTooLarge
		}
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			var parseErr *csv.ParseError
			if errors.As(err, &parseErr) {
				return rowErrors, fmt.Errorf("%w at row %d: %v", usecasestmt.ErrMalformedFile, parseErr.Line, parseErr.Err)
			}
			return rowErrors, fmt.Errorf("%w: %v", usecasestmt.ErrMalformedFile, err)
		}

		line, _ := reader.FieldPos(0)
		if line <= 0 {
			line = 1
		}

		totalRows++
		if totalRows > p.maxRows {
			return rowErrors, fmt.Errorf("%w: max %d rows allowed", usecasestmt.ErrTooManyRows, p.maxRows)
		}

		if !isValidUTF8Record(record) {
			if len(rowErrors) < usecasestmt.MaxCollectedRowErrors {
				rowErrors = append(rowErrors, usecasestmt.RowError{
					Row:     line,
					Message: "row contains invalid UTF-8 sequence",
				})
			}
			continue
		}

		tx, rowErr := normalizeRow(line, record, &accountNumber, &currency, nil)
		if rowErr != nil {
			if len(rowErrors) < usecasestmt.MaxCollectedRowErrors {
				rowErrors = append(rowErrors, *rowErr)
			}
			continue
		}

		validRows++
		batch = append(batch, tx)
		if len(batch) >= usecasestmt.DefaultChunkSize {
			if err := flushBatch(); err != nil {
				return rowErrors, err
			}
		}
	}

	if validRows == 0 && len(rowErrors) == 0 {
		return nil, usecasestmt.ErrEmptyStatement
	}

	if len(rowErrors) > 0 {
		return rowErrors, fmt.Errorf("%w: %d invalid rows", usecasestmt.ErrValidationFailed, len(rowErrors))
	}

	if err := flushBatch(); err != nil {
		return nil, err
	}

	return nil, nil
}

func isValidUTF8String(s string) bool {
	for i := 0; i < len(s); i++ {
		if s[i] >= 0x80 {
			return isValidUTF8Record([]string{s})
		}
	}
	return true
}
