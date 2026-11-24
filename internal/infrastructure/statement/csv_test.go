package statement_test

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/2inxs/openbanking-service/internal/domain/money"
	"github.com/2inxs/openbanking-service/internal/infrastructure/statement"
	usecasestmt "github.com/2inxs/openbanking-service/internal/usecase/statement"
)

func TestCSVParserParsesSampleFileAndIdenticalPurchases(t *testing.T) {
	f, err := os.Open(filepath.Join("testdata", "sample_statement.csv"))
	if err != nil {
		t.Fatalf("open sample_statement.csv: %v", err)
	}
	defer f.Close()

	got, err := statement.NewCSVParser().Parse(f)
	if err != nil {
		t.Fatalf("Parse failed: %v", err)
	}

	if got.AccountNumber != "40817810000000000001" || got.Currency != money.RUB {
		t.Fatalf("unexpected account/currency: %+v", got)
	}
	if len(got.Transactions) != 4 {
		t.Fatalf("len(Transactions) = %d, want 4", len(got.Transactions))
	}

	if got.Transactions[0].Value.Amount.String() != "125000.0000" {
		t.Fatalf("tx amount = %s, want 125000.0000", got.Transactions[0].Value.Amount)
	}
	if !got.Transactions[0].BookedAt.Equal(time.Date(2026, 11, 20, 0, 0, 0, 0, time.UTC)) {
		t.Fatalf("tx bookedAt = %v", got.Transactions[0].BookedAt)
	}

	tx1, tx2 := got.Transactions[1], got.Transactions[2]
	if tx1.ExternalID != "op-20261121-01" || tx2.ExternalID != "op-20261121-02" {
		t.Fatalf("identical purchases lost external IDs: %q, %q", tx1.ExternalID, tx2.ExternalID)
	}
	if tx1.Value.Amount.String() != "-350.0000" || tx2.Value.Amount.String() != "-350.0000" {
		t.Fatalf("identical purchases amounts mismatch: %s, %s", tx1.Value.Amount, tx2.Value.Amount)
	}
	if tx1.RowNumber != 3 || tx2.RowNumber != 4 {
		t.Fatalf("unexpected row numbers: %d, %d", tx1.RowNumber, tx2.RowNumber)
	}

	if got.Transactions[3].ExternalID != "" || got.Transactions[3].Description != "Подписка; онлайн-сервис" {
		t.Fatalf("unexpected tx: %+v", got.Transactions[3])
	}
}

func TestCSVParserSupportsUTF8BOMAndMultilineQuotedFields(t *testing.T) {
	raw := "\xEF\xBB\xBFaccount_number;booked_at;amount;currency;external_id;description;counterparty\n" +
		"40817810000000000002;2026-11-23T10:15:00+03:00;-99.95;USD;tx-1;\"Оплата\nпо счёту №1\";Shop\n"

	got, err := statement.NewCSVParser().Parse(strings.NewReader(raw))
	if err != nil {
		t.Fatalf("Parse with BOM and multiline failed: %v", err)
	}
	if len(got.Transactions) != 1 {
		t.Fatalf("expected 1 transaction, got %d", len(got.Transactions))
	}
	tx := got.Transactions[0]
	if tx.Value.Amount.String() != "-99.9500" || tx.Value.Currency != "USD" {
		t.Fatalf("unexpected value: %+v", tx.Value)
	}
	if !tx.BookedAt.Equal(time.Date(2026, 11, 23, 7, 15, 0, 0, time.UTC)) {
		t.Fatalf("BookedAt not normalized to UTC: %v", tx.BookedAt)
	}
	if tx.Description != "Оплата\nпо счёту №1" {
		t.Fatalf("multiline description broken: %q", tx.Description)
	}
}

func TestCSVParserRejectsEmptyAndHeaderOnlyFiles(t *testing.T) {
	parser := statement.NewCSVParser()

	for name, content := range map[string]string{
		"completely empty": "",
		"bom only":         "\xEF\xBB\xBF",
		"header only":      "account_number;booked_at;amount;currency;external_id;description;counterparty\n",
	} {
		t.Run(name, func(t *testing.T) {
			_, err := parser.Parse(strings.NewReader(content))
			if !errors.Is(err, usecasestmt.ErrEmptyStatement) {
				t.Fatalf("got err = %v, want ErrEmptyStatement", err)
			}
		})
	}
}

func TestCSVParserRejectsInvalidHeaderAndExtraColumns(t *testing.T) {
	parser := statement.NewCSVParser()

	wrongHeader := "acc;date;sum;cur;id;desc;cp\n40817810000000000001;2026-11-20;-10.00;RUB;1;a;b\n"
	if _, err := parser.Parse(strings.NewReader(wrongHeader)); !errors.Is(err, usecasestmt.ErrInvalidHeader) {
		t.Fatalf("expected ErrInvalidHeader, got %v", err)
	}

	extraCol := "account_number;booked_at;amount;currency;external_id;description;counterparty\n" +
		"40817810000000000001;2026-11-20;-10.00;RUB;1;a;b;extra\n"
	if _, err := parser.Parse(strings.NewReader(extraCol)); !errors.Is(err, usecasestmt.ErrMalformedFile) {
		t.Fatalf("expected ErrMalformedFile for extra column, got %v", err)
	}
}

func TestCSVParserReportsRowNumbersAndReasonsOnInvalidRows(t *testing.T) {
	raw := strings.Join([]string{
		"account_number;booked_at;amount;currency;external_id;description;counterparty",
		"40817810000000000001;2026-11-20;-100.00;RUB;tx-1;Валидная строка;Магазин",
		"40817810000000000001;20.11.2026;-100.00;RUB;tx-2;Неверная дата;Магазин",
		"40817810000000000001;2026-11-20;-100.12345;RUB;tx-3;Слишком много знаков;Магазин",
		"40817810000000000001;2026-11-20;-100.00;rub;tx-4;Неверная валюта;Магазин",
		"40817810000000000001;2026-11-20;0.00;RUB;tx-5;Нулевая сумма;Магазин",
		"40817810000000000999;2026-11-20;-50.00;RUB;tx-6;Чужой счёт в файле;Магазин",
		"40817810000000000001;2026-11-20;-50.00;USD;tx-7;Другая валюта в файле;Магазин",
	}, "\n")

	got, err := statement.NewCSVParser().Parse(strings.NewReader(raw))
	if !errors.Is(err, usecasestmt.ErrValidationFailed) {
		t.Fatalf("expected ErrValidationFailed, got %v", err)
	}
	if len(got.Transactions) != 1 {
		t.Fatalf("expected 1 valid transaction, got %d", len(got.Transactions))
	}
	if len(got.RowErrors) != 6 {
		t.Fatalf("expected 6 row errors, got %d: %+v", len(got.RowErrors), got.RowErrors)
	}

	wantRows := []int{3, 4, 5, 6, 7, 8}
	for i, wantRow := range wantRows {
		if got.RowErrors[i].Row != wantRow {
			t.Errorf("RowErrors[%d].Row = %d, want %d (msg=%q)", i, got.RowErrors[i].Row, wantRow, got.RowErrors[i].Message)
		}
		if got.RowErrors[i].Message == "" {
			t.Errorf("RowErrors[%d].Message is empty", i)
		}
	}
}
