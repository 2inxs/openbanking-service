//go:build integration

package integration_test

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/2inxs/openbanking-service/internal/domain"
	"github.com/2inxs/openbanking-service/internal/infrastructure/postgres"
	stmtinfra "github.com/2inxs/openbanking-service/internal/infrastructure/statement"
	usecasestmt "github.com/2inxs/openbanking-service/internal/usecase/statement"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

func isolatedStatementImportPool(t *testing.T) (*pgxpool.Pool, domain.AccountID) {
	t.Helper()
	ctx := context.Background()
	adminPool := repositoryPool(t)

	schema := "test_stmt_" + strings.ReplaceAll(newID(t), "-", "")
	userID := newID(t)
	bankID := newID(t)
	connID := newID(t)
	accountID := newID(t)

	setupSQL := fmt.Sprintf(`
CREATE SCHEMA %s;
CREATE TABLE %s.currencies (LIKE public.currencies INCLUDING ALL);
INSERT INTO %s.currencies SELECT * FROM public.currencies;
CREATE TABLE %s.users (LIKE public.users INCLUDING ALL);
CREATE TABLE %s.banks (LIKE public.banks INCLUDING ALL);
CREATE TABLE %s.bank_connections (LIKE public.bank_connections INCLUDING ALL);
CREATE TABLE %s.accounts (LIKE public.accounts INCLUDING ALL);
CREATE TABLE %s.statement_imports (LIKE public.statement_imports INCLUDING ALL);
CREATE TABLE %s.transactions (LIKE public.transactions INCLUDING ALL);
`, schema, schema, schema, schema, schema, schema, schema, schema, schema)

	if _, err := adminPool.Exec(ctx, setupSQL); err != nil {
		t.Fatalf("create isolated schema %s: %v", schema, err)
	}

	seedSQL := fmt.Sprintf(`
INSERT INTO %s.users (id, email, password_hash, display_name)
VALUES ('%s'::uuid, 'import@example.com', 'hash', 'Test User');
INSERT INTO %s.banks (id, code, name)
VALUES ('%s'::uuid, 'mockbank', 'Mock Bank');
INSERT INTO %s.bank_connections (id, user_id, bank_id, status)
VALUES ('%s'::uuid, '%s'::uuid, '%s'::uuid, 'active');
INSERT INTO %s.accounts (id, user_id, bank_id, bank_connection_id, external_account_id, account_number, name, type, status, currency_code)
VALUES ('%s'::uuid, '%s'::uuid, '%s'::uuid, '%s'::uuid, 'ext-acc-1', '40817810000000000001', 'Основной счёт', 'current', 'active', 'RUB');
`, schema, userID, schema, bankID, schema, connID, userID, bankID, schema, accountID, userID, bankID, connID)

	if _, err := adminPool.Exec(ctx, seedSQL); err != nil {
		t.Fatalf("seed isolated schema %s: %v", schema, err)
	}

	cfg := databaseConfig(t)
	poolCfg, err := pgxpool.ParseConfig(cfg.URL)
	if err != nil {
		t.Fatalf("parse pool config: %v", err)
	}
	poolCfg.MaxConns = 4
	poolCfg.AfterConnect = func(ctx context.Context, conn *pgx.Conn) error {
		_, err := conn.Exec(ctx, fmt.Sprintf(`SET search_path TO %s, public`, schema))
		return err
	}

	isolatedPool, err := pgxpool.NewWithConfig(ctx, poolCfg)
	if err != nil {
		t.Fatalf("open isolated pool: %v", err)
	}

	t.Cleanup(func() {
		isolatedPool.Close()
		if _, err := adminPool.Exec(context.Background(), fmt.Sprintf(`DROP SCHEMA IF EXISTS %s CASCADE`, schema)); err != nil {
			t.Errorf("drop isolated schema %s: %v", schema, err)
		}
	})

	return isolatedPool, domain.AccountID(accountID)
}

func TestStreamStatementImportCopyFromIdempotencyAndAtomicity(t *testing.T) {
	ctx := context.Background()
	pool, accountID := isolatedStatementImportPool(t)

	repo := postgres.NewStatementImportRepository(pool)
	svc := usecasestmt.NewImportService(stmtinfra.NewCSVParser(), stmtinfra.NewXLSXParser(), repo)

	const totalRows = 2505
	var sb strings.Builder
	sb.WriteString("account_number;booked_at;amount;currency;external_id;description;counterparty\n")
	for i := 1; i <= totalRows; i++ {
		fmt.Fprintf(&sb, "40817810000000000001;2026-11-20;-15.25;RUB;op-%d;Оплата %d;Магазин\n", i, i)
	}
	payload := sb.String()

	// 1. Первый двупоточный импорт 2 505 строк через CopyFrom.
	res1, err := svc.Import(ctx, usecasestmt.ImportCommand{
		AccountID: accountID,
		FileName:  "business_nov.csv",
		Reader:    strings.NewReader(payload),
	})
	if err != nil {
		t.Fatalf("first Import failed: %v", err)
	}
	if res1.ImportedCount != totalRows || res1.SkippedCount != 0 || res1.ImportID == "" {
		t.Fatalf("unexpected first ImportResult: %+v", res1)
	}

	// 2. Повторный импорт того же файла: все строки пропускаются без дублей.
	res2, err := svc.Import(ctx, usecasestmt.ImportCommand{
		AccountID: accountID,
		FileName:  "business_nov.csv",
		Reader:    strings.NewReader(payload),
	})
	if err != nil {
		t.Fatalf("second Import failed: %v", err)
	}
	if res2.ImportedCount != 0 || res2.SkippedCount != totalRows {
		t.Fatalf("unexpected second ImportResult (idempotency): %+v", res2)
	}

	var dbCount int
	if err := pool.QueryRow(ctx, `SELECT COUNT(*) FROM transactions WHERE account_id = $1::uuid`, string(accountID)).Scan(&dbCount); err != nil {
		t.Fatal(err)
	}
	if dbCount != totalRows {
		t.Fatalf("expected %d rows in transactions after re-import, got %d", totalRows, dbCount)
	}

	// 3. Ошибка на 2001-й строке (после двух успешных CopyFrom-чанков) полностью откатывает транзакцию.
	var broken strings.Builder
	broken.WriteString("account_number;booked_at;amount;currency;external_id;description;counterparty\n")
	for i := 1; i <= 2000; i++ {
		fmt.Fprintf(&broken, "40817810000000000001;2026-11-21;-10.00;RUB;new-op-%d;Новая %d;Магазин\n", i, i)
	}
	broken.WriteString("40817810000000000001;bad-date;-10.00;RUB;new-op-2001;Битая строка;Магазин\n")

	res3, err := svc.Import(ctx, usecasestmt.ImportCommand{
		AccountID: accountID,
		FileName:  "broken_tail.csv",
		Reader:    strings.NewReader(broken.String()),
	})
	if !errors.Is(err, usecasestmt.ErrValidationFailed) {
		t.Fatalf("expected ErrValidationFailed on broken tail, got %v", err)
	}
	if len(res3.RowErrors) != 1 || res3.RowErrors[0].Row != 2002 {
		t.Fatalf("expected RowError at line 2002, got %+v", res3.RowErrors)
	}

	// Количество строк в БД осталось строго 2505 — ни одна из 2000 строк нового файла не сохранилась.
	if err := pool.QueryRow(ctx, `SELECT COUNT(*) FROM transactions WHERE account_id = $1::uuid`, string(accountID)).Scan(&dbCount); err != nil {
		t.Fatal(err)
	}
	if dbCount != totalRows {
		t.Fatalf("atomicity violated: expected %d rows after rollback, got %d", totalRows, dbCount)
	}
	// 4. Пустой или несуществующий AccountID возвращает ErrAccountNotFound.
	if _, err := svc.Import(ctx, usecasestmt.ImportCommand{
		AccountID: "",
		FileName:  "business_nov.csv",
		Reader:    strings.NewReader(payload),
	}); !errors.Is(err, usecasestmt.ErrAccountNotFound) {
		t.Fatalf("expected ErrAccountNotFound for empty accountID, got %v", err)
	}
	if _, err := svc.Import(ctx, usecasestmt.ImportCommand{
		AccountID: "not-a-uuid",
		FileName:  "business_nov.csv",
		Reader:    strings.NewReader(payload),
	}); !errors.Is(err, usecasestmt.ErrAccountNotFound) {
		t.Fatalf("expected ErrAccountNotFound for malformed UUID, got %v", err)
	}
	// 5. Несовпадение номера счёта в выписке возвращает ErrAccountNumberMismatch.
	wrongAcc := "account_number;booked_at;amount;currency;external_id;description;counterparty\n" +
		"40817810000000009999;2026-11-20;-10.00;RUB;op-wrong-acc;Тест;Магазин\n"
	if _, err := svc.Import(ctx, usecasestmt.ImportCommand{
		AccountID: accountID,
		FileName:  "wrong_acc.csv",
		Reader:    strings.NewReader(wrongAcc),
	}); !errors.Is(err, usecasestmt.ErrAccountNumberMismatch) {
		t.Fatalf("expected ErrAccountNumberMismatch, got %v", err)
	}
	// 6. Несовпадение валюты выписки с валютой счёта возвращает ErrAccountCurrencyMismatch.
	wrongCurr := "account_number;booked_at;amount;currency;external_id;description;counterparty\n" +
		"40817810000000000001;2026-11-20;-10.00;USD;op-wrong-curr;Тест;Магазин\n"
	if _, err := svc.Import(ctx, usecasestmt.ImportCommand{
		AccountID: accountID,
		FileName:  "wrong_curr.csv",
		Reader:    strings.NewReader(wrongCurr),
	}); !errors.Is(err, usecasestmt.ErrAccountCurrencyMismatch) {
		t.Fatalf("expected ErrAccountCurrencyMismatch, got %v", err)
	}

	// 7. Два реальных одинаковых платежа без external_id в один день из двух разных выписок оба сохраняются без потерь.
	var beforeNoExt int
	if err := pool.QueryRow(ctx, `SELECT COUNT(*) FROM transactions WHERE account_id = $1::uuid`, string(accountID)).Scan(&beforeNoExt); err != nil {
		t.Fatal(err)
	}
	morningNoExt := "account_number;booked_at;amount;currency;external_id;description;counterparty\n" +
		"40817810000000000001;2026-11-22;-300.00;RUB;;Кофе;Кофейня\n"
	eveningNoExt := "account_number;booked_at;amount;currency;external_id;description;counterparty\n" +
		"40817810000000000001;2026-11-22;-300.00;RUB;;Кофе;Кофейня\n"

	rA, err := svc.Import(ctx, usecasestmt.ImportCommand{
		UserID:    ownerUserID,
		AccountID: accountID,
		FileName:  "morning_no_ext.csv",
		Reader:    strings.NewReader(morningNoExt),
	})
	if err != nil || rA.ImportedCount != 1 {
		t.Fatalf("first no-external-id payment import failed: err=%v res=%+v", err, rA)
	}
	rB, err := svc.Import(ctx, usecasestmt.ImportCommand{
		UserID:    ownerUserID,
		AccountID: accountID,
		FileName:  "evening_no_ext.csv",
		Reader:    strings.NewReader(eveningNoExt),
	})
	if err != nil || rB.ImportedCount != 1 {
		t.Fatalf("second identical no-external-id payment must not be lost: err=%v res=%+v", err, rB)
	}

	var afterNoExt int
	if err := pool.QueryRow(ctx, `SELECT COUNT(*) FROM transactions WHERE account_id = $1::uuid`, string(accountID)).Scan(&afterNoExt); err != nil {
		t.Fatal(err)
	}
	if afterNoExt != beforeNoExt+2 {
		t.Fatalf("expected +2 transactions for two separate no-external-id files, got %d (was %d)", afterNoExt, beforeNoExt)
	}
}
