package statement

import (
	"errors"
	"fmt"
	usecasestmt "github.com/2inxs/openbanking-service/internal/usecase/statement"
	"github.com/xuri/excelize/v2"
	"io"
	"strings"
	"time"
)

// MaxUnzippedBytes защищает от ZIP-бомб при распаковке XLSX-архива (50 МБ).
const MaxUnzippedBytes = 50 << 20

var ErrFormulaNotAllowed = errors.New("statement: formulas are not allowed in XLSX cells")

var _ usecasestmt.Parser = (*XLSXParser)(nil)

// XLSXParser разбирает банковскую выписку в строго зафиксированном формате XLSX.
type XLSXParser struct {
	maxRows       int
	maxUnzipBytes int64
}

func NewXLSXParser() *XLSXParser {
	return &XLSXParser{
		maxRows:       usecasestmt.MaxRows,
		maxUnzipBytes: MaxUnzippedBytes,
	}
}

func (p *XLSXParser) Parse(r io.Reader) (usecasestmt.ParsedStatement, error) {
	if r == nil {
		return usecasestmt.ParsedStatement{}, usecasestmt.ErrEmptyStatement
	}

	f, err := excelize.OpenReader(r, excelize.Options{
		UnzipSizeLimit:    p.maxUnzipBytes,
		UnzipXMLSizeLimit: p.maxUnzipBytes,
	})
	if err != nil {
		return usecasestmt.ParsedStatement{}, fmt.Errorf("%w: %v", usecasestmt.ErrMalformedFile, err)
	}
	defer func() { _ = f.Close() }()

	sheetName := f.GetSheetName(0)
	if sheetName == "" {
		return usecasestmt.ParsedStatement{}, usecasestmt.ErrEmptyStatement
	}

	rows, err := f.Rows(sheetName)
	if err != nil {
		return usecasestmt.ParsedStatement{}, fmt.Errorf("%w: read rows: %v", usecasestmt.ErrMalformedFile, err)
	}
	defer func() { _ = rows.Close() }()

	if !rows.Next() {
		if err := rows.Error(); err != nil {
			return usecasestmt.ParsedStatement{}, fmt.Errorf("%w: %v", usecasestmt.ErrMalformedFile, err)
		}
		return usecasestmt.ParsedStatement{}, usecasestmt.ErrEmptyStatement
	}

	header, err := rows.Columns()
	if err != nil {
		return usecasestmt.ParsedStatement{}, fmt.Errorf("%w: read header: %v", usecasestmt.ErrMalformedFile, err)
	}
	if err := validateHeader(header); err != nil {
		return usecasestmt.ParsedStatement{}, err
	}

	var stmt usecasestmt.ParsedStatement
	rowNumber := 1

	for rows.Next() {
		rowNumber++
		cols, err := rows.Columns()
		if err != nil {
			return usecasestmt.ParsedStatement{}, fmt.Errorf("%w at row %d: %v", usecasestmt.ErrMalformedFile, rowNumber, err)
		}

		if isEmptyRow(cols) {
			continue
		}

		if len(stmt.Transactions)+len(stmt.RowErrors) >= p.maxRows {
			return usecasestmt.ParsedStatement{}, fmt.Errorf("%w: max %d rows allowed", usecasestmt.ErrTooManyRows, p.maxRows)
		}

		if len(cols) > len(expectedStatementHeader) {
			stmt.RowErrors = append(stmt.RowErrors, usecasestmt.RowError{
				Row:     rowNumber,
				Message: fmt.Sprintf("expected %d columns, got %d", len(expectedStatementHeader), len(cols)),
			})
			continue
		}

		if formulaCol, hasFormula := p.rowHasFormula(f, sheetName, rowNumber, len(expectedStatementHeader)); hasFormula {
			stmt.RowErrors = append(stmt.RowErrors, usecasestmt.RowError{
				Row:     rowNumber,
				Message: fmt.Sprintf("%s (column %d)", ErrFormulaNotAllowed.Error(), formulaCol),
			})
			continue
		}

		record := make([]string, len(expectedStatementHeader))
		copy(record, cols)

		currentRow := rowNumber
		dateParser := func(raw string) (time.Time, error) {
			return parseXLSXDate(f, sheetName, currentRow, 2, raw)
		}

		tx, rowErr := normalizeRow(rowNumber, record, &stmt.AccountNumber, &stmt.Currency, dateParser)
		if rowErr != nil {
			stmt.RowErrors = append(stmt.RowErrors, *rowErr)
			continue
		}
		stmt.Transactions = append(stmt.Transactions, tx)
	}

	if err := rows.Error(); err != nil {
		return usecasestmt.ParsedStatement{}, fmt.Errorf("%w: %v", usecasestmt.ErrMalformedFile, err)
	}

	if len(stmt.Transactions) == 0 && len(stmt.RowErrors) == 0 {
		return usecasestmt.ParsedStatement{}, usecasestmt.ErrEmptyStatement
	}

	if len(stmt.RowErrors) > 0 {
		return stmt, fmt.Errorf("%w: %d invalid rows", usecasestmt.ErrValidationFailed, len(stmt.RowErrors))
	}

	return stmt, nil
}

func (p *XLSXParser) rowHasFormula(f *excelize.File, sheet string, row, maxCols int) (int, bool) {
	for col := 1; col <= maxCols; col++ {
		cell, err := excelize.CoordinatesToCellName(col, row)
		if err != nil {
			continue
		}
		if formula, err := f.GetCellFormula(sheet, cell); err == nil && strings.TrimSpace(formula) != "" {
			return col, true
		}
		if cellType, err := f.GetCellType(sheet, cell); err == nil && cellType == excelize.CellTypeFormula {
			return col, true
		}
	}
	return 0, false
}

func parseXLSXDate(f *excelize.File, sheet string, row, col int, displayVal string) (time.Time, error) {
	if t, err := parseStatementDate(displayVal); err == nil {
		return t, nil
	}
	cell, err := excelize.CoordinatesToCellName(col, row)
	if err != nil {
		return time.Time{}, fmt.Errorf("invalid booked_at %q", displayVal)
	}
	raw, err := f.GetCellValue(sheet, cell, excelize.Options{RawCellValue: true})
	if err != nil || strings.TrimSpace(raw) == "" {
		return time.Time{}, fmt.Errorf("invalid booked_at %q: expected YYYY-MM-DD, RFC3339 or Excel date", displayVal)
	}
	var serial float64
	if _, err := fmt.Sscanf(strings.TrimSpace(raw), "%f", &serial); err == nil && serial > 0 {
		t, err := excelize.ExcelDateToTime(serial, false)
		if err == nil {
			return time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, time.UTC), nil
		}
	}
	return time.Time{}, fmt.Errorf("invalid booked_at %q: expected YYYY-MM-DD, RFC3339 or Excel date", displayVal)
}

func isEmptyRow(cols []string) bool {
	for _, c := range cols {
		if strings.TrimSpace(c) != "" {
			return false
		}
	}
	return true
}
