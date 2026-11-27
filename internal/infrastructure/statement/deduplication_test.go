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

type captureUnicodeImporter struct {
	target    usecasestmt.ImportTargetAccount
	savedRows []usecasestmt.TransactionRow
}

func (c *captureUnicodeImporter) GetTargetAccount(_ context.Context, _ domain.UserID, _ domain.AccountID) (usecasestmt.ImportTargetAccount, error) {
	return c.target, nil
}

func (c *captureUnicodeImporter) ImportStream(
	_ context.Context,
	target usecasestmt.ImportTargetAccount,
	_ string,
	chunks <-chan usecasestmt.TransactionChunk,
	finalize func() (string, []usecasestmt.RowError, error),
) (usecasestmt.ImportResult, error) {
	for ch := range chunks {
		c.savedRows = append(c.savedRows, ch.Rows...)
	}
	if _, _, err := finalize(); err != nil {
		return usecasestmt.ImportResult{}, err
	}
	return usecasestmt.ImportResult{
		ImportID:      "imp-unicode",
		AccountID:     target.ID,
		ImportedCount: len(c.savedRows),
	}, nil
}

func TestUnicodeExternalIDPassesStatementImportAndDeduplication(t *testing.T) {
	accID := domain.AccountID("11111111-1111-1111-1111-111111111111")
	unicodeID := "ОПЕР-" + strings.Repeat("Ю", 200) // 205 рун, 410 байт (< 255 символов)

	csvContent := "account_number;booked_at;amount;currency;external_id;description;counterparty\n" +
		"40817810099910004312;2026-11-25;-500.00;RUB;" + unicodeID + ";Оплата услуг;ООО Тест\n"

	repo := &captureUnicodeImporter{
		target: usecasestmt.ImportTargetAccount{
			ID:            accID,
			AccountNumber: "40817810099910004312",
			Currency:      money.RUB,
		},
	}
	svc := usecasestmt.NewImportService(statement.NewCSVParser(), statement.NewXLSXParser(), repo)

	res, err := svc.Import(
		context.Background(),
		usecasestmt.ImportCommand{
			UserID:    "user-1",
			AccountID: accID,
			FileName:  "statement.csv",
			Reader:    strings.NewReader(csvContent),
		},
	)
	if err != nil {
		t.Fatalf("expected Unicode external_id import to succeed, got %v", err)
	}
	if res.ImportedCount != 1 || len(repo.savedRows) != 1 {
		t.Fatalf("expected 1 row to reach persistence, got res=%+v rows=%d", res, len(repo.savedRows))
	}

	var firstRow usecasestmt.TransactionRow
	for _, r := range repo.savedRows {
		firstRow = r
		break
	}
	key, strategy, err := statement.RowDeduplicationKey(accID, res.ImportID, firstRow)
	if err != nil {
		t.Fatalf("expected Unicode external_id to pass deduplication, got %v", err)
	}
	if strategy != domain.DedupStrategyExternalID || len(key) != 64 {
		t.Fatalf("unexpected dedup result: strategy=%q key=%q", strategy, key)
	}
}
