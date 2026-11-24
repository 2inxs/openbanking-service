package statement_test

import (
	"bytes"
	"errors"
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

func TestXLSXParserParsesValidFileAndIdenticalPurchases(t *testing.T) {
	rows := [][]any{
		{"account_number", "booked_at", "amount", "currency", "external_id", "description", "counterparty"},
		{"40817810000000000001", "2026-11-20", "125000.00", "RUB", "op-1", "Зарплата", "ООО Тест"},
		{"40817810000000000001", "2026-11-21", "-350.00", "RUB", "op-2", "Кофе", "Кофейня"},
		{"40817810000000000001", "2026-11-21", "-350.00", "RUB", "op-3", "Кофе", "Кофейня"},
	}

	got, err := statement.NewXLSXParser().Parse(buildXLSX(t, rows, nil))
	if err != nil {
		t.Fatalf("Parse failed: %v", err)
	}
	if got.AccountNumber != "40817810000000000001" || got.Currency != money.RUB {
		t.Fatalf("unexpected account/currency: %+v", got)
	}
	if len(got.Transactions) != 3 {
		t.Fatalf("len(Transactions) = %d, want 3", len(got.Transactions))
	}
	if got.Transactions[0].Value.Amount.String() != "125000.0000" {
		t.Fatalf("amount = %s, want 125000.0000", got.Transactions[0].Value.Amount)
	}
	if !got.Transactions[0].BookedAt.Equal(time.Date(2026, 11, 20, 0, 0, 0, 0, time.UTC)) {
		t.Fatalf("BookedAt = %v", got.Transactions[0].BookedAt)
	}
	if got.Transactions[1].ExternalID != "op-2" || got.Transactions[2].ExternalID != "op-3" {
		t.Fatalf("identical purchases lost IDs: %+v, %+v", got.Transactions[1], got.Transactions[2])
	}
}

func TestXLSXParserSupportsExcelSerialDate(t *testing.T) {
	rows := [][]any{
		{"account_number", "booked_at", "amount", "currency", "external_id", "description", "counterparty"},
		{"40817810000000000001", 46346, "-500.00", "RUB", "op-serial", "Покупка", "Магазин"},
	}

	got, err := statement.NewXLSXParser().Parse(buildXLSX(t, rows, nil))
	if err != nil {
		t.Fatalf("Parse with Excel serial date failed: %v", err)
	}
	if len(got.Transactions) != 1 || got.Transactions[0].BookedAt.IsZero() {
		t.Fatalf("expected 1 parsed transaction with UTC date, got %+v", got.Transactions)
	}
}

func TestXLSXParserRejectsEmptyHeaderOnlyAndCorruptedArchive(t *testing.T) {
	parser := statement.NewXLSXParser()

	if _, err := parser.Parse(bytes.NewReader(nil)); !errors.Is(err, usecasestmt.ErrMalformedFile) {
		t.Fatalf("empty reader: got %v, want ErrMalformedFile", err)
	}
	if _, err := parser.Parse(bytes.NewReader([]byte("not a zip archive"))); !errors.Is(err, usecasestmt.ErrMalformedFile) {
		t.Fatalf("corrupted archive: got %v, want ErrMalformedFile", err)
	}

	emptySheet := buildXLSX(t, nil, nil)
	if _, err := parser.Parse(emptySheet); !errors.Is(err, usecasestmt.ErrEmptyStatement) {
		t.Fatalf("empty sheet: got %v, want ErrEmptyStatement", err)
	}

	headerOnly := buildXLSX(t, [][]any{
		{"account_number", "booked_at", "amount", "currency", "external_id", "description", "counterparty"},
	}, nil)
	if _, err := parser.Parse(headerOnly); !errors.Is(err, usecasestmt.ErrEmptyStatement) {
		t.Fatalf("header only: got %v, want ErrEmptyStatement", err)
	}
}

func TestXLSXParserRejectsFormulasAndInvalidRows(t *testing.T) {
	rows := [][]any{
		{"account_number", "booked_at", "amount", "currency", "external_id", "description", "counterparty"},
		{"40817810000000000001", "2026-11-20", "100.00", "RUB", "tx-1", "Формула в сумме", "Shop"},
		{"40817810000000000001", "bad-date", "100.00", "RUB", "tx-2", "Неверная дата", "Shop"},
		{"40817810000000000001", "2026-11-20", "10.12345", "RUB", "tx-3", "Лишние знаки", "Shop"},
		{"40817810000000000001", "2026-11-20", "10.00", "eur", "tx-4", "Неверная валюта", "Shop"},
	}
	buf := buildXLSX(t, rows, map[string]string{
		"C2": "SUM(10,20)",
	})

	got, err := statement.NewXLSXParser().Parse(buf)
	if !errors.Is(err, usecasestmt.ErrValidationFailed) {
		t.Fatalf("expected ErrValidationFailed, got %v", err)
	}
	if len(got.Transactions) != 0 {
		t.Fatalf("expected 0 valid rows, got %d", len(got.Transactions))
	}
	if len(got.RowErrors) != 4 {
		t.Fatalf("expected 4 row errors, got %d: %+v", len(got.RowErrors), got.RowErrors)
	}
	for i, wantRow := range []int{2, 3, 4, 5} {
		if got.RowErrors[i].Row != wantRow {
			t.Errorf("RowErrors[%d].Row = %d, want %d", i, got.RowErrors[i].Row, wantRow)
		}
	}
}
