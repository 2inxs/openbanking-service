package statement_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/2inxs/openbanking-service/internal/domain"
	"github.com/2inxs/openbanking-service/internal/domain/money"
	"github.com/2inxs/openbanking-service/internal/infrastructure/statement"
	"github.com/2inxs/openbanking-service/internal/usecase/banksync"
	usecasestmt "github.com/2inxs/openbanking-service/internal/usecase/statement"
)

func TestCrossSourceDeduplicationBetweenAPIAndStatements(t *testing.T) {
	accID := domain.AccountID("11111111-1111-1111-1111-111111111111")
	amt, err := money.Parse("-1250.00")
	if err != nil {
		t.Fatal(err)
	}
	tAPI := time.Date(2026, 11, 18, 10, 0, 0, 0, time.UTC)
	tStmt := time.Date(2026, 11, 18, 0, 0, 0, 0, time.UTC)

	apiTx := banksync.BankTransaction{
		ExternalID:  "op-987654",
		Status:      domain.TransactionBooked,
		Value:       money.Money{Amount: amt, Currency: money.RUB},
		BookedAt:    &tAPI,
		Description: "Оплата по карте (API)",
	}

	stmtRow1 := usecasestmt.TransactionRow{
		RowNumber:   3,
		ExternalID:  "op-987654",
		BookedAt:    tStmt,
		Value:       money.Money{Amount: amt, Currency: money.RUB},
		Description: "Оплата по карте (Выписка 1)",
	}

	stmtRowOverlap := usecasestmt.TransactionRow{
		RowNumber:   1,
		ExternalID:  "op-987654",
		BookedAt:    tStmt,
		Value:       money.Money{Amount: amt, Currency: money.RUB},
		Description: "Оплата по карте (Пересекающаяся выписка 2)",
	}

	apiKey, _, err := banksync.TransactionDeduplicationKey(accID, apiTx)
	if err != nil {
		t.Fatal(err)
	}
	stmtKey1, _, err := statement.RowDeduplicationKey(accID, "imp-1", stmtRow1)
	if err != nil {
		t.Fatal(err)
	}
	stmtKey2, _, err := statement.RowDeduplicationKey(accID, "imp-2", stmtRowOverlap)
	if err != nil {
		t.Fatal(err)
	}

	if apiKey != stmtKey1 {
		t.Fatalf("API -> statement and statement -> API must yield identical key: api=%s stmt=%s", apiKey, stmtKey1)
	}
	if stmtKey1 != stmtKey2 {
		t.Fatalf("overlapping statements with same external_id must yield identical key: k1=%s k2=%s", stmtKey1, stmtKey2)
	}
}

func TestUnicodeExternalIDPassesStatementParseToDeduplicationKey(t *testing.T) {
	accID := domain.AccountID("11111111-1111-1111-1111-111111111111")
	unicodeID := "ОПЕР-" + strings.Repeat("Ю", 200) // 205 рун, 410 байт (< 255 символов)

	csvContent := "account_number,booked_at,amount,currency,external_id,description\n" +
		"40817810099910004312,2026-11-25,-500.00,RUB," + unicodeID + ",Оплата услуг\n"

	parser := statement.NewCSVParser()
	chunks, finalize := parser.ParseStream(context.Background(), strings.NewReader(csvContent), usecasestmt.ParseOptions{
		ExpectedCurrency: money.RUB,
	})

	var rows []usecasestmt.TransactionRow
	for ch := range chunks {
		rows = append(rows, ch.Rows...)
	}
	if _, rowErrs, err := finalize(); err != nil || len(rowErrs) != 0 {
		t.Fatalf("expected valid CSV parse for Unicode external_id, err=%v rowErrs=%v", err, rowErrs)
	}
	if len(rows) != 1 {
		t.Fatalf("expected 1 parsed row, got %d", len(rows))
	}
	key, strategy, err := statement.RowDeduplicationKey(accID, "imp-unicode", rows[0])
	if err != nil {
		t.Fatalf("expected Unicode external_id to pass deduplication, got %v", err)
	}
	if strategy != domain.DedupStrategyExternalID || len(key) != 64 {
		t.Fatalf("unexpected dedup result: strategy=%q key=%q", strategy, key)
	}
}
