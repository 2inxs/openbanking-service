package statement

import (
	"archive/zip"
	"bytes"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"math"
	"strconv"
	"strings"
	"time"

	usecasestmt "github.com/2inxs/openbanking-service/internal/usecase/statement"
	"github.com/xuri/excelize/v2"
)

const MaxUnzippedBytes = 50 << 20

const (
	minExcelSerialDate = 1.0
	maxExcelSerialDate = 2958465.99999
)

var ErrFormulaNotAllowed = errors.New("statement: formulas are not allowed in XLSX cells")

var _ usecasestmt.Parser = (*XLSXParser)(nil)

type XLSXParser struct {
	maxFileSize   int64
	maxRows       int
	maxUnzipBytes int64
}

func NewXLSXParser() *XLSXParser {
	return &XLSXParser{
		maxFileSize:   usecasestmt.MaxFileSize,
		maxRows:       usecasestmt.MaxRows,
		maxUnzipBytes: MaxUnzippedBytes,
	}
}

func (p *XLSXParser) Parse(r io.Reader) (usecasestmt.ParsedStatement, error) {
	if r == nil {
		return usecasestmt.ParsedStatement{}, usecasestmt.ErrEmptyStatement
	}

	rawBytes, err := io.ReadAll(io.LimitReader(r, p.maxFileSize+1))
	if err != nil {
		return usecasestmt.ParsedStatement{}, fmt.Errorf("%w: %v", usecasestmt.ErrMalformedFile, err)
	}
	if int64(len(rawBytes)) > p.maxFileSize {
		return usecasestmt.ParsedStatement{}, usecasestmt.ErrFileTooLarge
	}
	if len(rawBytes) == 0 {
		return usecasestmt.ParsedStatement{}, usecasestmt.ErrEmptyStatement
	}

	if err := validateFirstSheetXML(rawBytes, p.maxUnzipBytes); err != nil {
		return usecasestmt.ParsedStatement{}, err
	}

	f, err := excelize.OpenReader(bytes.NewReader(rawBytes), excelize.Options{
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

	wbProps, err := f.GetWorkbookProps()
	if err != nil {
		return usecasestmt.ParsedStatement{}, fmt.Errorf("%w: read workbook props: %v", usecasestmt.ErrMalformedFile, err)
	}
	date1904 := wbProps.Date1904 != nil && *wbProps.Date1904

	allRows, err := f.GetRows(sheetName)
	if err != nil {
		return usecasestmt.ParsedStatement{}, fmt.Errorf("%w: read rows: %v", usecasestmt.ErrMalformedFile, err)
	}
	if len(allRows) == 0 {
		return usecasestmt.ParsedStatement{}, usecasestmt.ErrEmptyStatement
	}

	displayHeader := trimTrailingEmpty(allRows[0])
	if len(displayHeader) != len(expectedStatementHeader) {
		return usecasestmt.ParsedStatement{}, fmt.Errorf("%w: expected %d columns, got %d", usecasestmt.ErrInvalidHeader, len(expectedStatementHeader), len(displayHeader))
	}
	header, err := readRawRow(f, sheetName, 1, len(expectedStatementHeader))
	if err != nil {
		return usecasestmt.ParsedStatement{}, fmt.Errorf("%w: read header: %v", usecasestmt.ErrMalformedFile, err)
	}
	if err := validateHeader(header); err != nil {
		return usecasestmt.ParsedStatement{}, err
	}

	var stmt usecasestmt.ParsedStatement

	for idx := 1; idx < len(allRows); idx++ {
		rowNumber := idx + 1
		displayCols := trimTrailingEmpty(allRows[idx])

		rawCols, err := readRawRow(f, sheetName, rowNumber, len(expectedStatementHeader))
		if err != nil {
			return usecasestmt.ParsedStatement{}, fmt.Errorf("%w at row %d: %v", usecasestmt.ErrMalformedFile, rowNumber, err)
		}

		if isEmptyRow(displayCols) && isEmptyRow(rawCols) {
			continue
		}

		if len(stmt.Transactions)+len(stmt.RowErrors) >= p.maxRows {
			return usecasestmt.ParsedStatement{}, fmt.Errorf("%w: max %d rows allowed", usecasestmt.ErrTooManyRows, p.maxRows)
		}

		if len(displayCols) > len(expectedStatementHeader) {
			stmt.RowErrors = append(stmt.RowErrors, usecasestmt.RowError{
				Row:     rowNumber,
				Message: fmt.Sprintf("expected %d columns, got %d", len(expectedStatementHeader), len(displayCols)),
			})
			continue
		}

		formulaCol, hasFormula, err := p.rowHasFormula(f, sheetName, rowNumber, len(expectedStatementHeader))
		if err != nil {
			return usecasestmt.ParsedStatement{}, fmt.Errorf("%w at row %d: %v", usecasestmt.ErrMalformedFile, rowNumber, err)
		}
		if hasFormula {
			stmt.RowErrors = append(stmt.RowErrors, usecasestmt.RowError{
				Row:     rowNumber,
				Message: fmt.Sprintf("%s (column %d)", ErrFormulaNotAllowed.Error(), formulaCol),
			})
			continue
		}

		currentRow := rowNumber
		dateParser := func(raw string) (time.Time, error) {
			return parseXLSXDate(f, sheetName, currentRow, 2, raw, date1904)
		}

		tx, rowErr := normalizeRow(rowNumber, rawCols, &stmt.AccountNumber, &stmt.Currency, dateParser)
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

func trimTrailingEmpty(cols []string) []string {
	end := len(cols)
	for end > 0 && strings.TrimSpace(cols[end-1]) == "" {
		end--
	}
	return cols[:end]
}

func validateFirstSheetXML(archive []byte, maxUnzipBytes int64) error {
	zr, err := zip.NewReader(bytes.NewReader(archive), int64(len(archive)))
	if err != nil {
		return fmt.Errorf("%w: %v", usecasestmt.ErrMalformedFile, err)
	}

	var totalUncompressed uint64
	var sheetFile *zip.File
	for _, zf := range zr.File {
		totalUncompressed += zf.UncompressedSize64
		if totalUncompressed > uint64(maxUnzipBytes) {
			return fmt.Errorf("%w: uncompressed size exceeds limit", usecasestmt.ErrMalformedFile)
		}
		if zf.Name == "xl/worksheets/sheet1.xml" {
			sheetFile = zf
		}
	}
	if sheetFile == nil {
		for _, zf := range zr.File {
			if strings.HasPrefix(zf.Name, "xl/worksheets/") && strings.HasSuffix(zf.Name, ".xml") {
				sheetFile = zf
				break
			}
		}
	}
	if sheetFile == nil {
		return fmt.Errorf("%w: worksheet XML not found", usecasestmt.ErrMalformedFile)
	}

	rc, err := sheetFile.Open()
	if err != nil {
		return fmt.Errorf("%w: open worksheet XML: %v", usecasestmt.ErrMalformedFile, err)
	}
	defer func() { _ = rc.Close() }()

	dec := xml.NewDecoder(io.LimitReader(rc, maxUnzipBytes+1))
	for {
		_, err := dec.Token()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return fmt.Errorf("%w: truncated or invalid worksheet XML: %v", usecasestmt.ErrMalformedFile, err)
		}
	}
	return nil
}

func readRawRow(f *excelize.File, sheet string, row, maxCols int) ([]string, error) {
	cols := make([]string, maxCols)
	for col := 1; col <= maxCols; col++ {
		cell, err := excelize.CoordinatesToCellName(col, row)
		if err != nil {
			return nil, err
		}
		val, err := f.GetCellValue(sheet, cell, excelize.Options{RawCellValue: true})
		if err != nil {
			return nil, err
		}
		cols[col-1] = val
	}
	return cols, nil
}

func (p *XLSXParser) rowHasFormula(f *excelize.File, sheet string, row, maxCols int) (int, bool, error) {
	for col := 1; col <= maxCols; col++ {
		cell, err := excelize.CoordinatesToCellName(col, row)
		if err != nil {
			return 0, false, err
		}
		formula, err := f.GetCellFormula(sheet, cell)
		if err != nil {
			return 0, false, err
		}
		if strings.TrimSpace(formula) != "" {
			return col, true, nil
		}
		cellType, err := f.GetCellType(sheet, cell)
		if err != nil {
			return 0, false, err
		}
		if cellType == excelize.CellTypeFormula {
			return col, true, nil
		}
	}
	return 0, false, nil
}

func parseXLSXDate(f *excelize.File, sheet string, row, col int, rawVal string, date1904 bool) (time.Time, error) {
	s := strings.TrimSpace(rawVal)
	if s == "" {
		return time.Time{}, errors.New("booked_at is required")
	}

	cell, err := excelize.CoordinatesToCellName(col, row)
	if err != nil {
		return time.Time{}, fmt.Errorf("invalid booked_at %q", rawVal)
	}
	cellType, err := f.GetCellType(sheet, cell)
	if err != nil {
		return time.Time{}, fmt.Errorf("invalid booked_at %q: %v", rawVal, err)
	}

	switch cellType {
	case excelize.CellTypeSharedString, excelize.CellTypeInlineString:
		return parseStatementDate(s)
	case excelize.CellTypeUnset, excelize.CellTypeNumber, excelize.CellTypeDate:
		if t, err := parseStatementDate(s); err == nil {
			return t, nil
		}
		serial, err := strconv.ParseFloat(s, 64)
		if err != nil || math.IsNaN(serial) || math.IsInf(serial, 0) {
			return time.Time{}, fmt.Errorf("invalid booked_at %q: expected YYYY-MM-DD, RFC3339 or Excel date", rawVal)
		}
		if serial < minExcelSerialDate || serial > maxExcelSerialDate {
			return time.Time{}, fmt.Errorf("invalid booked_at %q: Excel serial date out of range", rawVal)
		}
		t, err := excelize.ExcelDateToTime(serial, date1904)
		if err != nil {
			return time.Time{}, fmt.Errorf("invalid booked_at %q: %v", rawVal, err)
		}
		return t.UTC(), nil
	default:
		return time.Time{}, fmt.Errorf("invalid booked_at %q: unsupported cell type", rawVal)
	}
}

func isEmptyRow(cols []string) bool {
	for _, c := range cols {
		if strings.TrimSpace(c) != "" {
			return false
		}
	}
	return true
}
