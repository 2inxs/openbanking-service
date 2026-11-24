package statement_test

import (
	"archive/zip"
	"bytes"
	"errors"
	"io"
	"testing"
	"time"

	"github.com/2inxs/openbanking-service/internal/domain/money"
	"github.com/2inxs/openbanking-service/internal/infrastructure/statement"
	usecasestmt "github.com/2inxs/openbanking-service/internal/usecase/statement"
	"github.com/xuri/excelize/v2"
)

func buildXLSX(t *testing.T, rows [][]any, formulas map[string]string) *bytes.Buffer {
	t.Helper()
	f := excelize.NewFile()
	defer func() { _ = f.Close() }()

	sheet := f.GetSheetName(0)
	for rIdx, row := range rows {
		for cIdx, val := range row {
			cell, err := excelize.CoordinatesToCellName(cIdx+1, rIdx+1)
			if err != nil {
				t.Fatal(err)
			}
			if err := f.SetCellValue(sheet, cell, val); err != nil {
				t.Fatal(err)
			}
		}
	}
	for cell, formula := range formulas {
		if err := f.SetCellFormula(sheet, cell, formula); err != nil {
			t.Fatal(err)
		}
	}

	buf, err := f.WriteToBuffer()
	if err != nil {
		t.Fatal(err)
	}
	return buf
}

func TestBothParsersImplementSharedInterface(t *testing.T) {
	parsers := map[string]usecasestmt.Parser{
		"csv":  statement.NewCSVParser(),
		"xlsx": statement.NewXLSXParser(),
	}
	for name, p := range parsers {
		t.Run(name, func(t *testing.T) {
			if _, err := p.Parse(bytes.NewReader(nil)); !errors.Is(err, usecasestmt.ErrEmptyStatement) {
				t.Fatalf("empty input: got %v, want ErrEmptyStatement", err)
			}
		})
	}
}

func TestXLSXParserPreservesRawAmountSignAndPrecisionDespiteNumberFormat(t *testing.T) {
	f := excelize.NewFile()
	defer func() { _ = f.Close() }()
	sheet := f.GetSheetName(0)

	headers := []string{"account_number", "booked_at", "amount", "currency", "external_id", "description", "counterparty"}
	for i, h := range headers {
		cell, _ := excelize.CoordinatesToCellName(i+1, 1)
		_ = f.SetCellValue(sheet, cell, h)
	}

	// Строка 2: сумма 123.4567 с форматом 2 знаков после запятой (отображается как 123.46, но должна остаться 123.4567).
	_ = f.SetCellValue(sheet, "A2", "40817810000000000001")
	_ = f.SetCellValue(sheet, "B2", "2026-11-20")
	_ = f.SetCellValue(sheet, "C2", 123.4567)
	_ = f.SetCellValue(sheet, "D2", "RUB")
	_ = f.SetCellValue(sheet, "E2", "op-round")
	styleTwoDecimals, err := f.NewStyle(&excelize.Style{NumFmt: 2}) // 0.00
	if err != nil {
		t.Fatal(err)
	}
	_ = f.SetCellStyle(sheet, "C2", "C2", styleTwoDecimals)

	// Строка 3: отрицательная сумма -123.45 с форматом "0.00;0.00", скрывающим минус.
	_ = f.SetCellValue(sheet, "A3", "40817810000000000001")
	_ = f.SetCellValue(sheet, "B3", "2026-11-21")
	_ = f.SetCellValue(sheet, "C3", -123.45)
	_ = f.SetCellValue(sheet, "D3", "RUB")
	_ = f.SetCellValue(sheet, "E3", "op-neg")
	customFmt := "0.00;0.00"
	styleHideMinus, err := f.NewStyle(&excelize.Style{CustomNumFmt: &customFmt})
	if err != nil {
		t.Fatal(err)
	}
	_ = f.SetCellStyle(sheet, "C3", "C3", styleHideMinus)

	buf, err := f.WriteToBuffer()
	if err != nil {
		t.Fatal(err)
	}

	got, err := statement.NewXLSXParser().Parse(buf)
	if err != nil {
		t.Fatalf("Parse failed: %v", err)
	}
	if len(got.Transactions) != 2 {
		t.Fatalf("expected 2 transactions, got %d", len(got.Transactions))
	}
	if got.Transactions[0].Value.Amount.String() != "123.4567" {
		t.Fatalf("rounded display format corrupted amount: got %s, want 123.4567", got.Transactions[0].Value.Amount)
	}
	if got.Transactions[1].Value.Amount.String() != "-123.4500" {
		t.Fatalf("hidden minus format lost sign: got %s, want -123.4500", got.Transactions[1].Value.Amount)
	}
}

func TestXLSXParserRejectsHiddenExcessDecimals(t *testing.T) {
	f := excelize.NewFile()
	defer func() { _ = f.Close() }()
	sheet := f.GetSheetName(0)

	headers := []string{"account_number", "booked_at", "amount", "currency", "external_id", "description", "counterparty"}
	for i, h := range headers {
		cell, _ := excelize.CoordinatesToCellName(i+1, 1)
		_ = f.SetCellValue(sheet, cell, h)
	}
	_ = f.SetCellValue(sheet, "A2", "40817810000000000001")
	_ = f.SetCellValue(sheet, "B2", "2026-11-20")
	_ = f.SetCellValue(sheet, "C2", 10.12345) // 5 дробных знаков скрыты форматом 0.00
	_ = f.SetCellValue(sheet, "D2", "RUB")
	styleTwoDecimals, _ := f.NewStyle(&excelize.Style{NumFmt: 2})
	_ = f.SetCellStyle(sheet, "C2", "C2", styleTwoDecimals)

	buf, _ := f.WriteToBuffer()
	got, err := statement.NewXLSXParser().Parse(buf)
	if !errors.Is(err, usecasestmt.ErrValidationFailed) {
		t.Fatalf("expected ErrValidationFailed for 5 decimals hidden by format, got %v", err)
	}
	if len(got.RowErrors) != 1 || got.RowErrors[0].Row != 2 {
		t.Fatalf("unexpected RowErrors: %+v", got.RowErrors)
	}
}

func toExcelSerial(t time.Time, date1904 bool) float64 {
	epoch := time.Date(1899, 12, 30, 0, 0, 0, 0, time.UTC)
	if date1904 {
		epoch = time.Date(1904, 1, 1, 0, 0, 0, 0, time.UTC)
	}
	return float64(t.UTC().Sub(epoch)) / float64(24*time.Hour)
}

func TestXLSXParserSupportsBothCalendarsAndPreservesTime(t *testing.T) {
	// 1. Стандартный календарь 1900 года: серийная дата с временем 2026-11-20 12:30:00 UTC.
	wantTime := time.Date(2026, 11, 20, 12, 30, 0, 0, time.UTC)
	serial1900 := toExcelSerial(wantTime, false)

	rows := [][]any{
		{"account_number", "booked_at", "amount", "currency", "external_id", "description", "counterparty"},
		{"40817810000000000001", serial1900, "-500.00", "RUB", "op-1900", "Покупка", "Магазин"},
	}
	got1900, err := statement.NewXLSXParser().Parse(buildXLSX(t, rows, nil))
	if err != nil {
		t.Fatalf("Parse 1900 calendar failed: %v", err)
	}
	if !got1900.Transactions[0].BookedAt.Equal(wantTime) {
		t.Fatalf("1900 BookedAt = %v, want %v", got1900.Transactions[0].BookedAt, wantTime)
	}

	// 2. Календарь 1904 года (Date1904 = true).
	f := excelize.NewFile()
	defer func() { _ = f.Close() }()
	date1904 := true
	if err := f.SetWorkbookProps(&excelize.WorkbookPropsOptions{Date1904: &date1904}); err != nil {
		t.Fatal(err)
	}
	serial1904 := toExcelSerial(wantTime, true)
	sheet := f.GetSheetName(0)
	headers := []string{"account_number", "booked_at", "amount", "currency", "external_id", "description", "counterparty"}
	for i, h := range headers {
		cell, _ := excelize.CoordinatesToCellName(i+1, 1)
		_ = f.SetCellValue(sheet, cell, h)
	}
	_ = f.SetCellValue(sheet, "A2", "40817810000000000001")
	_ = f.SetCellValue(sheet, "B2", serial1904)
	_ = f.SetCellValue(sheet, "C2", "-500.00")
	_ = f.SetCellValue(sheet, "D2", "RUB")

	buf1904, _ := f.WriteToBuffer()
	got1904, err := statement.NewXLSXParser().Parse(buf1904)
	if err != nil {
		t.Fatalf("Parse 1904 calendar failed: %v", err)
	}
	if !got1904.Transactions[0].BookedAt.Equal(wantTime) {
		t.Fatalf("1904 BookedAt = %v, want %v", got1904.Transactions[0].BookedAt, wantTime)
	}
}

func TestXLSXParserRejectsInvalidAndJunkDates(t *testing.T) {
	rows := [][]any{
		{"account_number", "booked_at", "amount", "currency", "external_id", "description", "counterparty"},
		{"40817810000000000001", "20.11.2026", "100.00", "RUB", "tx-1", "Точки вместо дефисов", "Shop"},
		{"40817810000000000001", "46346junk", "100.00", "RUB", "tx-2", "Мусор после числа", "Shop"},
		{"40817810000000000001", "", "100.00", "RUB", "tx-3", "Пустая дата", "Shop"},
		{"40817810000000000001", -10, "100.00", "RUB", "tx-4", "Отрицательная серийная дата", "Shop"},
		{"40817810000000000001", 9999999, "100.00", "RUB", "tx-5", "Серийная дата вне диапазона", "Shop"},
	}

	got, err := statement.NewXLSXParser().Parse(buildXLSX(t, rows, nil))
	if !errors.Is(err, usecasestmt.ErrValidationFailed) {
		t.Fatalf("expected ErrValidationFailed, got %v", err)
	}
	if len(got.Transactions) != 0 || len(got.RowErrors) != 5 {
		t.Fatalf("expected 0 valid and 5 errors, got valid=%d errors=%+v", len(got.Transactions), got.RowErrors)
	}
}

func TestXLSXParserRejectsTruncatedSheetXMLInsideValidZIP(t *testing.T) {
	validBuf := buildXLSX(t, [][]any{
		{"account_number", "booked_at", "amount", "currency", "external_id", "description", "counterparty"},
		{"40817810000000000001", "2026-11-20", "100.00", "RUB", "tx-1", "Операция 1", "Shop"},
		{"40817810000000000001", "2026-11-21", "-50.00", "RUB", "tx-2", "Операция 2", "Shop"},
	}, nil)

	zr, err := zip.NewReader(bytes.NewReader(validBuf.Bytes()), int64(validBuf.Len()))
	if err != nil {
		t.Fatal(err)
	}

	var truncatedZip bytes.Buffer
	zw := zip.NewWriter(&truncatedZip)
	for _, zf := range zr.File {
		rc, err := zf.Open()
		if err != nil {
			t.Fatal(err)
		}
		data, err := io.ReadAll(rc)
		_ = rc.Close()
		if err != nil {
			t.Fatal(err)
		}

		if zf.Name == "xl/worksheets/sheet1.xml" {
			// Обрезаем XML сразу после первой строки данных, без закрывающих тегов </sheetData></worksheet>.
			idx := bytes.Index(data, []byte("</row>"))
			if idx == -1 {
				t.Fatal("closing row tag not found in sheet1.xml")
			}
			secondIdx := bytes.Index(data[idx+6:], []byte("</row>"))
			if secondIdx == -1 {
				t.Fatal("second row tag not found in sheet1.xml")
			}
			data = data[:idx+6+secondIdx+6]
		}

		w, err := zw.Create(zf.Name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := w.Write(data); err != nil {
			t.Fatal(err)
		}
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}

	if _, err := statement.NewXLSXParser().Parse(bytes.NewReader(truncatedZip.Bytes())); !errors.Is(err, usecasestmt.ErrMalformedFile) {
		t.Fatalf("truncated XML inside ZIP: got %v, want ErrMalformedFile", err)
	}
}

func TestXLSXParserRejectsOversizedInputAndZipBomb(t *testing.T) {
	parser := statement.NewXLSXParser()

	// 1. Превышение лимита входного размера 10 МБ до распаковки.
	oversized := bytes.NewReader(make([]byte, usecasestmt.MaxFileSize+1))
	if _, err := parser.Parse(oversized); !errors.Is(err, usecasestmt.ErrFileTooLarge) {
		t.Fatalf("oversized input: got %v, want ErrFileTooLarge", err)
	}

	// 2. ZIP-бомба: сжатый файл < 10 МБ, но распакованный XML > 50 МБ.
	var bomb bytes.Buffer
	zw := zip.NewWriter(&bomb)
	w, err := zw.Create("xl/worksheets/sheet1.xml")
	if err != nil {
		t.Fatal(err)
	}
	chunk := bytes.Repeat([]byte(" "), 1<<20) // 1 МБ пробелов сжимается в ~1 КБ
	for i := 0; i < 52; i++ {
		if _, err := w.Write(chunk); err != nil {
			t.Fatal(err)
		}
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	if bomb.Len() >= usecasestmt.MaxFileSize {
		t.Fatalf("bomb compressed size %d should be < 10MB", bomb.Len())
	}
	if _, err := parser.Parse(bytes.NewReader(bomb.Bytes())); !errors.Is(err, usecasestmt.ErrMalformedFile) {
		t.Fatalf("zip bomb: got %v, want ErrMalformedFile", err)
	}
}
func TestXLSXParserRejectsFormulasAndInvalidRows(t *testing.T) {
	rows := [][]any{
		{"account_number", "booked_at", "amount", "currency", "external_id", "description", "counterparty"},
		{"40817810000000000001", "2026-11-20", "100.00", "RUB", "tx-1", "Формула в сумме", "Shop"},
		{"40817810000000000001", "2026-11-20", "-350.00", "RUB", "op-2", "Кофе", "Кофейня"},
		{"40817810000000000001", "2026-11-20", "-350.00", "RUB", "op-3", "Кофе", "Кофейня"},
	}
	buf := buildXLSX(t, rows, map[string]string{
		"C2": "SUM(10,20)",
	})
	got, err := statement.NewXLSXParser().Parse(buf)
	if !errors.Is(err, usecasestmt.ErrValidationFailed) {
		t.Fatalf("expected ErrValidationFailed, got %v", err)
	}
	if len(got.Transactions) != 2 || got.Currency != money.RUB {
		t.Fatalf("expected 2 identical valid purchases, got %+v", got.Transactions)
	}
	if len(got.RowErrors) != 1 || got.RowErrors[0].Row != 2 {
		t.Fatalf("expected RowError on row 2, got %+v", got.RowErrors)
	}
}

func TestXLSXParserPreservesRowCoordinatesAcrossSkippedEmptyRows(t *testing.T) {
	rows := [][]any{
		{"account_number", "booked_at", "amount", "currency", "external_id", "description", "counterparty"},
		nil,
		{"40817810000000000001", "2026-11-20", "-250.00", "RUB", "op-row-3", "Операция на 3-й строке", "Shop"},
	}

	got, err := statement.NewXLSXParser().Parse(buildXLSX(t, rows, nil))
	if err != nil {
		t.Fatalf("Parse with skipped empty row failed: %v", err)
	}
	if len(got.Transactions) != 1 || got.Transactions.RowNumber != 3 || got.Transactions.ExternalID != "op-row-3" {
		t.Fatalf("unexpected row mapping across empty row: %+v", got.Transactions)
	}
}
