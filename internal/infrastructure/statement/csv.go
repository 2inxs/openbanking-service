package statement

import (
	"bufio"
	"bytes"
	"encoding/csv"
	"errors"
	"fmt"
	usecasestmt "github.com/2inxs/openbanking-service/internal/usecase/statement"
	"io"
)

const (
	CSVDelimiter        = ';'
	CSVDecimalSeparator = '.'
	CSVEncoding         = "utf-8"
)

var _ usecasestmt.Parser = (*CSVParser)(nil)

// CSVParser разбирает банковскую выписку в строго зафиксированном CSV-формате.
type CSVParser struct {
	maxRows int
}

func NewCSVParser() *CSVParser {
	return &CSVParser{maxRows: usecasestmt.MaxRows}
}

func (p *CSVParser) Parse(r io.Reader) (usecasestmt.ParsedStatement, error) {
	if r == nil {
		return usecasestmt.ParsedStatement{}, usecasestmt.ErrEmptyStatement
	}

	br := bufio.NewReader(r)
	if err := stripUTF8BOM(br); err != nil {
		return usecasestmt.ParsedStatement{}, fmt.Errorf("%w: %v", usecasestmt.ErrMalformedFile, err)
	}

	reader := csv.NewReader(br)
	reader.Comma = CSVDelimiter
	reader.FieldsPerRecord = len(expectedStatementHeader)
	reader.ReuseRecord = false

	header, err := reader.Read()
	if err != nil {
		if errors.Is(err, io.EOF) {
			return usecasestmt.ParsedStatement{}, usecasestmt.ErrEmptyStatement
		}
		return usecasestmt.ParsedStatement{}, fmt.Errorf("%w: %v", usecasestmt.ErrMalformedFile, err)
	}
	if err := validateHeader(header); err != nil {
		return usecasestmt.ParsedStatement{}, err
	}

	var stmt usecasestmt.ParsedStatement
	rowNumber := 1

	for {
		record, err := reader.Read()
		if errors.Is(err, io.EOF) {
			break
		}
		rowNumber++

		if err != nil {
			var parseErr *csv.ParseError
			if errors.As(err, &parseErr) {
				return usecasestmt.ParsedStatement{}, fmt.Errorf("%w at row %d: %v", usecasestmt.ErrMalformedFile, rowNumber, parseErr.Err)
			}
			return usecasestmt.ParsedStatement{}, fmt.Errorf("%w at row %d: %v", usecasestmt.ErrMalformedFile, rowNumber, err)
		}

		if len(stmt.Transactions)+len(stmt.RowErrors) >= p.maxRows {
			return usecasestmt.ParsedStatement{}, fmt.Errorf("%w: max %d rows allowed", usecasestmt.ErrTooManyRows, p.maxRows)
		}

		tx, rowErr := normalizeRow(rowNumber, record, &stmt.AccountNumber, &stmt.Currency, nil)
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
