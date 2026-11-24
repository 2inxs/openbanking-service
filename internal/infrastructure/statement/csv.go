package statement

import (
	"bufio"
	"bytes"
	"encoding/csv"
	"errors"
	"fmt"
	"io"
	"unicode/utf8"

	usecasestmt "github.com/2inxs/openbanking-service/internal/usecase/statement"
)

const (
	CSVDelimiter        = ';'
	CSVDecimalSeparator = '.'
	CSVEncoding         = "utf-8"
)

var _ usecasestmt.Parser = (*CSVParser)(nil)

type CSVParser struct {
	maxFileSize int64
	maxRows     int
}

func NewCSVParser() *CSVParser {
	return &CSVParser{
		maxFileSize: usecasestmt.MaxFileSize,
		maxRows:     usecasestmt.MaxRows,
	}
}

func (p *CSVParser) Parse(r io.Reader) (usecasestmt.ParsedStatement, error) {
	if r == nil {
		return usecasestmt.ParsedStatement{}, usecasestmt.ErrEmptyStatement
	}

	limited := &limitedReader{r: r, remaining: p.maxFileSize}
	br := bufio.NewReader(limited)
	if err := stripUTF8BOM(br); err != nil {
		if errors.Is(err, usecasestmt.ErrFileTooLarge) {
			return usecasestmt.ParsedStatement{}, err
		}
		return usecasestmt.ParsedStatement{}, fmt.Errorf("%w: %v", usecasestmt.ErrMalformedFile, err)
	}

	reader := csv.NewReader(br)
	reader.Comma = CSVDelimiter
	reader.FieldsPerRecord = len(expectedStatementHeader)
	reader.ReuseRecord = false

	header, err := reader.Read()
	if err != nil {
		if limited.exceeded || errors.Is(err, usecasestmt.ErrFileTooLarge) {
			return usecasestmt.ParsedStatement{}, usecasestmt.ErrFileTooLarge
		}
		if errors.Is(err, io.EOF) {
			return usecasestmt.ParsedStatement{}, usecasestmt.ErrEmptyStatement
		}
		return usecasestmt.ParsedStatement{}, fmt.Errorf("%w: %v", usecasestmt.ErrMalformedFile, err)
	}
	for i, col := range header {
		if !utf8.ValidString(col) {
			return usecasestmt.ParsedStatement{}, fmt.Errorf("%w: column %d contains invalid UTF-8", usecasestmt.ErrInvalidHeader, i+1)
		}
	}
	if err := validateHeader(header); err != nil {
		return usecasestmt.ParsedStatement{}, err
	}

	var stmt usecasestmt.ParsedStatement

	for {
		record, err := reader.Read()
		if limited.exceeded || errors.Is(err, usecasestmt.ErrFileTooLarge) {
			return usecasestmt.ParsedStatement{}, usecasestmt.ErrFileTooLarge
		}
		if errors.Is(err, io.EOF) {
			break
		}

		line, _ := reader.FieldPos(0)
		if line <= 0 {
			line = 1
		}

		if err != nil {
			var parseErr *csv.ParseError
			if errors.As(err, &parseErr) {
				return usecasestmt.ParsedStatement{}, fmt.Errorf("%w at row %d: %v", usecasestmt.ErrMalformedFile, parseErr.Line, parseErr.Err)
			}
			return usecasestmt.ParsedStatement{}, fmt.Errorf("%w at row %d: %v", usecasestmt.ErrMalformedFile, line, err)
		}

		if len(stmt.Transactions)+len(stmt.RowErrors) >= p.maxRows {
			return usecasestmt.ParsedStatement{}, fmt.Errorf("%w: max %d rows allowed", usecasestmt.ErrTooManyRows, p.maxRows)
		}

		if !isValidUTF8Record(record) {
			stmt.RowErrors = append(stmt.RowErrors, usecasestmt.RowError{
				Row:     line,
				Message: "row contains invalid UTF-8 sequence",
			})
			continue
		}

		tx, rowErr := normalizeRow(line, record, &stmt.AccountNumber, &stmt.Currency, nil)
		if rowErr != nil {
			stmt.RowErrors = append(stmt.RowErrors, *rowErr)
			continue
		}
		stmt.Transactions = append(stmt.Transactions, tx)
	}

	if len(stmt.Transactions) == 0 && len(stmt.RowErrors) == 0 {
		return usecasestmt.ParsedStatement{}, usecasestmt.ErrEmptyStatement
	}

	if len(stmt.RowErrors) > 0 {
		return stmt, fmt.Errorf("%w: %d invalid rows", usecasestmt.ErrValidationFailed, len(stmt.RowErrors))
	}

	return stmt, nil
}

type limitedReader struct {
	r         io.Reader
	remaining int64
	exceeded  bool
}

func (l *limitedReader) Read(p []byte) (int, error) {
	if l.remaining < 0 {
		l.exceeded = true
		return 0, usecasestmt.ErrFileTooLarge
	}
	if int64(len(p)) > l.remaining+1 {
		p = p[:l.remaining+1]
	}
	n, err := l.r.Read(p)
	l.remaining -= int64(n)
	if l.remaining < 0 {
		l.exceeded = true
		return n, usecasestmt.ErrFileTooLarge
	}
	return n, err
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

func isValidUTF8Record(record []string) bool {
	for _, field := range record {
		if !utf8.ValidString(field) {
			return false
		}
	}
	return true
}
