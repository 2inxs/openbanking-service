package statement

import (
	"bufio"
	"bytes"
	"context"
	"encoding/csv"
	"errors"
	"fmt"
	"io"
	"unicode/utf8"

	"github.com/2inxs/openbanking-service/internal/domain/money"
	usecasestmt "github.com/2inxs/openbanking-service/internal/usecase/statement"
)

const (
	streamReadBufferSize = 64 << 10 // 64 КБ буфер чтения
	maxCSVLineBytes      = 64 << 10 // 64 КБ максимум на одну строку выписки (защита от OOM)
	maxVarchar255Runes   = 255
)

var ErrLineTooLong = errors.New("statement: single CSV line exceeds 64 KB limit")

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

func (p *CSVParser) ParseStream(ctx context.Context, r io.Reader, chunks chan<- usecasestmt.TransactionChunk) ([]usecasestmt.RowError, error) {
	defer close(chunks)

	if r == nil {
		return nil, usecasestmt.ErrEmptyStatement
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	limited := &limitedReader{r: r, remaining: p.maxFileSize}
	lineGuarded := &maxLineBytesReader{r: limited, maxLine: maxCSVLineBytes}
	br := bufio.NewReaderSize(lineGuarded, streamReadBufferSize)
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
		if lineGuarded.exceeded || errors.Is(err, ErrLineTooLong) {
			return nil, fmt.Errorf("%w: %v", usecasestmt.ErrMalformedFile, ErrLineTooLong)
		}
		if errors.Is(err, io.EOF) {
			return nil, usecasestmt.ErrEmptyStatement
		}
		return nil, fmt.Errorf("%w: %v", usecasestmt.ErrMalformedFile, err)
	}
	for i, col := range header {
		if !utf8.ValidString(col) {
			return nil, fmt.Errorf("%w: column %d contains invalid UTF-8", usecasestmt.ErrInvalidHeader, i+1)
		}
	}
	if err := validateHeader(header); err != nil {
		return nil, err
	}

	var (
		accountNumber  string
		currency       money.Currency
		validRows      int
		totalRowErrors int
		rowErrors      []usecasestmt.RowError
		batch          = make([]usecasestmt.TransactionRow, 0, usecasestmt.DefaultChunkSize)
	)

	addRowError := func(row int, msg string) {
		totalRowErrors++
		if len(rowErrors) < usecasestmt.MaxCollectedRowErrors {
			rowErrors = append(rowErrors, usecasestmt.RowError{Row: row, Message: msg})
		}
	}

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
		if lineGuarded.exceeded || errors.Is(err, ErrLineTooLong) {
			return rowErrors, fmt.Errorf("%w: %v", usecasestmt.ErrMalformedFile, ErrLineTooLong)
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

		if validRows+totalRowErrors >= p.maxRows {
			return rowErrors, fmt.Errorf("%w: max %d rows allowed", usecasestmt.ErrTooManyRows, p.maxRows)
		}

		if !isValidUTF8Record(record) {
			addRowError(line, "row contains invalid UTF-8 sequence")
			continue
		}

		tx, rowErr := normalizeRow(line, record, &accountNumber, &currency, nil)
		if rowErr != nil {
			addRowError(rowErr.Row, rowErr.Message)
			continue
		}
		if utf8.RuneCountInString(tx.ExternalID) > maxVarchar255Runes {
			addRowError(line, "external_id exceeds 255 characters")
			continue
		}
		if utf8.RuneCountInString(tx.CounterpartyName) > maxVarchar255Runes {
			addRowError(line, "counterparty exceeds 255 characters")
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

	if validRows == 0 && totalRowErrors == 0 {
		return nil, usecasestmt.ErrEmptyStatement
	}

	if totalRowErrors > 0 {
		return rowErrors, fmt.Errorf("%w: %d invalid rows", usecasestmt.ErrValidationFailed, totalRowErrors)
	}

	if err := flushBatch(); err != nil {
		return nil, err
	}

	return nil, nil
}

type maxLineBytesReader struct {
	r           io.Reader
	maxLine     int
	currentLine int
	exceeded    bool
}

func (m *maxLineBytesReader) Read(p []byte) (int, error) {
	if m.exceeded {
		return 0, ErrLineTooLong
	}
	n, err := m.r.Read(p)
	chunk := p[:n]
	for len(chunk) > 0 {
		nl := bytes.IndexByte(chunk, '\n')
		if nl == -1 {
			m.currentLine += len(chunk)
			if m.currentLine > m.maxLine {
				m.exceeded = true
				return n, ErrLineTooLong
			}
			break
		}
		m.currentLine += nl
		if m.currentLine > m.maxLine {
			m.exceeded = true
			return n, ErrLineTooLong
		}
		m.currentLine = 0
		chunk = chunk[nl+1:]
	}
	return n, err
}
