package postgres

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/2inxs/openbanking-service/internal/domain"
	"github.com/2inxs/openbanking-service/internal/domain/money"
	usecasestmt "github.com/2inxs/openbanking-service/internal/usecase/statement"
	"github.com/jackc/pgx/v5"
)

type txBeginner interface {
	Begin(ctx context.Context) (pgx.Tx, error)
}

type StatementImportRepository struct {
	db DBTX
}

var _ usecasestmt.ImportRepository = (*StatementImportRepository)(nil)

func NewStatementImportRepository(db DBTX) *StatementImportRepository {
	return &StatementImportRepository{db: db}
}

func (r *StatementImportRepository) GetTargetAccount(ctx context.Context, accountID domain.AccountID) (usecasestmt.ImportTargetAccount, error) {
	var (
		idStr    string
		accNum   string
		currCode string
	)
	err := r.db.QueryRow(ctx, `SELECT id::text, account_number, currency_code
 FROM accounts
 WHERE id = $1::uuid`, string(accountID)).Scan(&idStr, &accNum, &currCode)
	if err != nil {
		return usecasestmt.ImportTargetAccount{}, repositoryError(err, usecasestmt.ErrAccountNotFound, domain.ErrInvalidModel)
	}
	return usecasestmt.ImportTargetAccount{
		ID:            domain.AccountID(idStr),
		AccountNumber: accNum,
		Currency:      money.Currency(currCode),
	}, nil
}

func (r *StatementImportRepository) ImportStream(
	ctx context.Context,
	target usecasestmt.ImportTargetAccount,
	fileName string,
	chunks <-chan usecasestmt.TransactionChunk,
	finalize func() (fileSHA256 string, rowErrs []usecasestmt.RowError, streamErr error),
) (usecasestmt.ImportResult, error) {
	beginner, ok := r.db.(txBeginner)
	if !ok {
		return usecasestmt.ImportResult{}, ErrUnavailable
	}

	var (
		tx      pgx.Tx
		txReady bool
	)
	defer func() {
		if tx != nil {
			_ = tx.Rollback(ctx)
		}
	}()

	ensureTx := func() error {
		if txReady {
			return nil
		}
		var err error
		tx, err = beginner.Begin(ctx)
		if err != nil {
			return repositoryError(err, usecasestmt.ErrAccountNotFound, domain.ErrInvalidModel)
		}
		_, err = tx.Exec(ctx, `CREATE TEMP TABLE tmp_statement_tx (
external_transaction_id varchar(255),
amount numeric(20,4) NOT NULL,
currency_code varchar(3) NOT NULL,
occurred_at timestamptz NOT NULL,
booked_at timestamptz NOT NULL,
description text,
counterparty_name varchar(255),
deduplication_key varchar(64) NOT NULL
) ON COMMIT DROP`)
		if err != nil {
			return repositoryError(err, usecasestmt.ErrAccountNotFound, domain.ErrInvalidModel)
		}
		txReady = true
		return nil
	}

	// Счётчик повторов идентичных бесномерных операций внутри одного файла,
	// чтобы две реальные одинаковые покупки за день обе импортировались, но повторный импорт файла отсекался.
	inFileOccurrence := make(map[string]int)
	var totalReceived int

	copyColumns := []string{
		"external_transaction_id",
		"amount",
		"currency_code",
		"occurred_at",
		"booked_at",
		"description",
		"counterparty_name",
		"deduplication_key",
	}

	for chunk := range chunks {
		if len(chunk.Rows) == 0 {
			continue
		}
		if err := ensureTx(); err != nil {
			return usecasestmt.ImportResult{}, err
		}
		if strings.TrimSpace(chunk.AccountNumber) != strings.TrimSpace(target.AccountNumber) {
			return usecasestmt.ImportResult{}, fmt.Errorf("%w: got %q, want %q", usecasestmt.ErrAccountNumberMismatch, chunk.AccountNumber, target.AccountNumber)
		}
		if chunk.Currency != target.Currency {
			return usecasestmt.ImportResult{}, fmt.Errorf("%w: got %q, want %q", usecasestmt.ErrAccountCurrencyMismatch, chunk.Currency, target.Currency)
		}

		copyRows := make([][]any, 0, len(chunk.Rows))
		for _, row := range chunk.Rows {
			dedupKey := computeStatementDedupKey(target.ID, row, inFileOccurrence)
			var extID any
			if strings.TrimSpace(row.ExternalID) != "" {
				extID = strings.TrimSpace(row.ExternalID)
			}
			var desc any
			if row.Description != "" {
				desc = row.Description
			}
			var cp any
			if row.CounterpartyName != "" {
				cp = row.CounterpartyName
			}
			bookedUTC := row.BookedAt.UTC()
			copyRows = append(copyRows, []any{
				extID,
				row.Value.Amount.String(),
				string(row.Value.Currency),
				bookedUTC,
				bookedUTC,
				desc,
				cp,
				dedupKey,
			})
		}

		if _, err := tx.CopyFrom(ctx, pgx.Identifier{"tmp_statement_tx"}, copyColumns, pgx.CopyFromRows(copyRows)); err != nil {
			return usecasestmt.ImportResult{}, repositoryError(err, usecasestmt.ErrAccountNotFound, domain.ErrInvalidModel)
		}
		totalReceived += len(chunk.Rows)
	}

	// Дожидаемся завершения Producer-горутины и проверяем итог разбора всего потока.
	fileSHA256, rowErrs, streamErr := finalize()
	if streamErr != nil {
		return usecasestmt.ImportResult{
			AccountID: target.ID,
			RowErrors: rowErrs,
		}, streamErr
	}
	if totalReceived == 0 {
		return usecasestmt.ImportResult{}, usecasestmt.ErrEmptyStatement
	}

	safeFileName := strings.TrimSpace(fileName)
	if safeFileName == "" {
		safeFileName = "statement.csv"
	}
	if len(safeFileName) > 255 {
		safeFileName = safeFileName[:255]
	}

	var importID string
	err = tx.QueryRow(ctx, `INSERT INTO statement_imports (account_id, file_name, file_sha256, status)
 VALUES ($1::uuid, $2, $3, 'processing')
 RETURNING id::text`, string(target.ID), safeFileName, fileSHA256).Scan(&importID)
	if err != nil {
		return usecasestmt.ImportResult{}, repositoryError(err, usecasestmt.ErrAccountNotFound, domain.ErrInvalidModel)
	}

	// Переносим уникальные записи из временной таблицы в transactions одним SQL-запросом
	// с защитой от дубликатов как внутри файла (DISTINCT ON), так и с уже загруженными операциями (ON CONFLICT).
	tag, err := tx.Exec(ctx, `INSERT INTO transactions (
account_id,
statement_import_id,
external_transaction_id,
source,
status,
amount,
currency_code,
occurred_at,
booked_at,
description,
counterparty_name,
deduplication_key
)
SELECT DISTINCT ON (deduplication_key)
$1::uuid,
$2::uuid,
external_transaction_id,
'statement',
'booked',
amount,
currency_code,
occurred_at,
booked_at,
description,
counterparty_name,
deduplication_key
FROM tmp_statement_tx
ORDER BY deduplication_key
ON CONFLICT (account_id, deduplication_key) DO NOTHING`, string(target.ID), importID)
	if err != nil {
		return usecasestmt.ImportResult{}, repositoryError(err, usecasestmt.ErrAccountNotFound, domain.ErrInvalidModel)
	}

	importedCount := int(tag.RowsAffected())
	skippedCount := totalReceived - importedCount
	if skippedCount < 0 {
		skippedCount = 0
	}

	_, err = tx.Exec(ctx, `UPDATE statement_imports
 SET status = 'completed',
     imported_count = $2,
     skipped_count = $3,
     error_count = 0,
     completed_at = now(),
     updated_at = now()
 WHERE id = $1::uuid`, importID, importedCount, skippedCount)
	if err != nil {
		return usecasestmt.ImportResult{}, repositoryError(err, usecasestmt.ErrAccountNotFound, domain.ErrInvalidModel)
	}

	if err := tx.Commit(ctx); err != nil {
		return usecasestmt.ImportResult{}, repositoryError(err, usecasestmt.ErrAccountNotFound, domain.ErrInvalidModel)
	}

	return usecasestmt.ImportResult{
		ImportID:      domain.StatementImportID(importID),
		AccountID:     target.ID,
		ImportedCount: importedCount,
		SkippedCount:  skippedCount,
	}, nil
}

func computeStatementDedupKey(accountID domain.AccountID, row usecasestmt.TransactionRow, occurrences map[string]int) string {
	ext := strings.TrimSpace(row.ExternalID)
	if ext != "" {
		sum := sha256.Sum256([]byte(fmt.Sprintf("v1|%s|ext|%s", accountID, ext)))
		return hex.EncodeToString(sum[:])
	}
	base := fmt.Sprintf("v1|%s|row|%s|%s|%s|%s|%s",
		accountID,
		row.BookedAt.UTC().Format(time.RFC3339Nano),
		row.Value.Amount.String(),
		row.Value.Currency,
		strings.TrimSpace(row.Description),
		strings.TrimSpace(row.CounterpartyName),
	)
	occurrences[base]++
	sum := sha256.Sum256([]byte(fmt.Sprintf("%s|occ|%d", base, occurrences[base])))
	return hex.EncodeToString(sum[:])
}

var _ = errors.Is
