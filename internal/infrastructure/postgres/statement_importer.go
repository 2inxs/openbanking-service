package postgres

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
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

func isValidUUIDString(s string) bool {
	if len(s) != 36 {
		return false
	}
	for i := 0; i < len(s); i++ {
		c := s[i]
		if i == 8 || i == 13 || i == 18 || i == 23 {
			if c != '-' {
				return false
			}
			continue
		}
		isHex := (c >= '0' && c <= '9') || (c >= 'a' && c <= 'f') || (c >= 'A' && c <= 'F')
		if !isHex {
			return false
		}
	}
	return true
}

func (r *StatementImportRepository) GetTargetAccount(ctx context.Context, userID domain.UserID, accountID domain.AccountID) (usecasestmt.ImportTargetAccount, error) {
	rawUserID := strings.TrimSpace(string(userID))
	rawAccID := strings.TrimSpace(string(accountID))
	if !isValidUUIDString(rawUserID) || !isValidUUIDString(rawAccID) {
		return usecasestmt.ImportTargetAccount{}, usecasestmt.ErrAccountNotFound
	}
	var (
		idStr      string
		accNum     *string
		currCode   string
		status     string
		isArchived bool
	)
	err := r.db.QueryRow(ctx,
		"SELECT id::text, account_number, currency_code, status, is_archived FROM accounts WHERE id = $1::uuid AND user_id = $2::uuid",
		rawAccID, rawUserID,
	).Scan(&idStr, &accNum, &currCode, &status, &isArchived)
	if err != nil {
		return usecasestmt.ImportTargetAccount{}, repositoryError(err, usecasestmt.ErrAccountNotFound, domain.ErrInvalidModel)
	}
	if isArchived || status == "closed" {
		return usecasestmt.ImportTargetAccount{}, fmt.Errorf("%w: target account is closed or archived", domain.ErrInvalidModel)
	}
	var number string
	if accNum != nil {
		number = *accNum
	}
	return usecasestmt.ImportTargetAccount{
		ID:            domain.AccountID(idStr),
		AccountNumber: number,
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

	log := usecasestmt.LoggerFromContext(ctx)

	var (
		tx      pgx.Tx
		txReady bool
	)
	defer func() {
		if tx != nil {
			_ = tx.Rollback(ctx)
		}
	}()

	createTempSQL := "CREATE TEMP TABLE tmp_statement_tx (" +
		"external_transaction_id varchar(255), " +
		"amount numeric(20,4) NOT NULL, " +
		"currency_code varchar(3) NOT NULL, " +
		"occurred_at timestamptz NOT NULL, " +
		"booked_at timestamptz NOT NULL, " +
		"description text, " +
		"counterparty_name varchar(255), " +
		"deduplication_key varchar(64) NOT NULL" +
		") ON COMMIT DROP"

	ensureTx := func() error {
		if txReady {
			return nil
		}
		var err error
		tx, err = beginner.Begin(ctx)
		if err != nil {
			return repositoryError(err, usecasestmt.ErrAccountNotFound, domain.ErrInvalidModel)
		}
		var lockedID string
		if err = tx.QueryRow(ctx, "SELECT id::text FROM accounts WHERE id = $1::uuid FOR UPDATE", string(target.ID)).Scan(&lockedID); err != nil {
			return repositoryError(err, usecasestmt.ErrAccountNotFound, domain.ErrInvalidModel)
		}
		if _, err = tx.Exec(ctx, createTempSQL); err != nil {
			return repositoryError(err, usecasestmt.ErrAccountNotFound, domain.ErrInvalidModel)
		}
		txReady = true
		return nil
	}

	var importID string
	var (
		totalReceived int
		chunkIndex    int
	)

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
		if strings.TrimSpace(target.AccountNumber) != "" &&
			strings.TrimSpace(chunk.AccountNumber) != strings.TrimSpace(target.AccountNumber) {
			return usecasestmt.ImportResult{}, fmt.Errorf("%w: got %q, want %q", usecasestmt.ErrAccountNumberMismatch, chunk.AccountNumber, target.AccountNumber)
		}
		if chunk.Currency != target.Currency {
			return usecasestmt.ImportResult{}, fmt.Errorf("%w: got %q, want %q", usecasestmt.ErrAccountCurrencyMismatch, chunk.Currency, target.Currency)
		}

		chunkIndex++
		copyStart := time.Now()

		copyRows := make([][]any, 0, len(chunk.Rows))
		for _, row := range chunk.Rows {
			dedupKey := computeStatementDedupKey(target.ID, importID, row)
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

		log.DebugContext(ctx, "statement import chunk copied",
			slog.Int("chunk_index", chunkIndex),
			slog.Int("rows_in_chunk", len(chunk.Rows)),
			slog.Int("total_copied", totalReceived),
			slog.Int64("copy_duration_ms", time.Since(copyStart).Milliseconds()),
		)
	}

	fileSHA256, rowErrs, streamErr := finalize()
	if streamErr != nil {
		return usecasestmt.ImportResult{
			AccountID: target.ID,
			RowErrors: rowErrs,
		}, streamErr
	}
	if totalReceived == 0 || !txReady {
		return usecasestmt.ImportResult{}, usecasestmt.ErrEmptyStatement
	}

	safeFileName := strings.TrimSpace(fileName)
	if safeFileName == "" {
		safeFileName = "statement.csv"
	}
	if len(safeFileName) > 255 {
		safeFileName = safeFileName[:255]
	}

	// Атомарно резервируем запись в statement_imports с защитой от гонки параллельных загрузок того же файла.
	insertImportSQL := "INSERT INTO statement_imports (account_id, file_name, file_sha256, status) " +
		"VALUES ($1::uuid, $2, $3, 'processing') " +
		"ON CONFLICT (account_id, file_sha256) WHERE status <> 'failed' DO NOTHING " +
		"RETURNING id::text"
	err := tx.QueryRow(ctx, insertImportSQL, string(target.ID), safeFileName, fileSHA256).Scan(&importID)
	if errors.Is(err, pgx.ErrNoRows) {
		var existingImportID string
		findExistingSQL := "SELECT id::text FROM statement_imports " +
			"WHERE account_id = $1::uuid AND file_sha256 = $2 AND status <> 'failed' LIMIT 1"
		if findErr := tx.QueryRow(ctx, findExistingSQL, string(target.ID), fileSHA256).Scan(&existingImportID); findErr != nil {
			return usecasestmt.ImportResult{}, repositoryError(findErr, usecasestmt.ErrAccountNotFound, domain.ErrInvalidModel)
		}
		if commitErr := tx.Commit(ctx); commitErr != nil {
			return usecasestmt.ImportResult{}, repositoryError(commitErr, usecasestmt.ErrAccountNotFound, domain.ErrInvalidModel)
		}
		log.InfoContext(ctx, "statement import deduplicated by file_sha256",
			slog.String("import_id", existingImportID),
			slog.String("file_sha256", fileSHA256),
			slog.Int("skipped_count", totalReceived),
		)
		return usecasestmt.ImportResult{
			ImportID:      domain.StatementImportID(existingImportID),
			AccountID:     target.ID,
			ImportedCount: 0,
			SkippedCount:  totalReceived,
		}, nil
	}
	if err != nil {
		return usecasestmt.ImportResult{}, repositoryError(err, usecasestmt.ErrAccountNotFound, domain.ErrInvalidModel)
	}

	mergeStart := time.Now()
	mergeSQL := "INSERT INTO transactions (" +
		"account_id, statement_import_id, external_transaction_id, source, status, " +
		"amount, currency_code, occurred_at, booked_at, description, counterparty_name, deduplication_key" +
		") SELECT DISTINCT ON (tmp.deduplication_key) " +
		"$1::uuid, $2::uuid, tmp.external_transaction_id, 'statement', 'booked', " +
		"tmp.amount, tmp.currency_code, tmp.occurred_at, tmp.booked_at, tmp.description, tmp.counterparty_name, tmp.deduplication_key " +
		"FROM tmp_statement_tx tmp " +
		"WHERE tmp.external_transaction_id IS NULL OR NOT EXISTS (" +
		"SELECT 1 FROM transactions ex WHERE ex.account_id = $1::uuid AND ex.external_transaction_id = tmp.external_transaction_id" +
		") " +
		"ORDER BY tmp.deduplication_key " +
		"ON CONFLICT (account_id, deduplication_key) DO NOTHING"
	tag, err := tx.Exec(ctx, mergeSQL, string(target.ID), importID)
	if err != nil {
		return usecasestmt.ImportResult{}, repositoryError(err, usecasestmt.ErrAccountNotFound, domain.ErrInvalidModel)
	}

	importedCount := int(tag.RowsAffected())
	skippedCount := totalReceived - importedCount
	if skippedCount < 0 {
		skippedCount = 0
	}

	log.DebugContext(ctx, "statement import merge completed",
		slog.String("import_id", importID),
		slog.Int("chunks_total", chunkIndex),
		slog.Int("imported_count", importedCount),
		slog.Int("skipped_count", skippedCount),
		slog.Int64("merge_duration_ms", time.Since(mergeStart).Milliseconds()),
	)

	updateImportSQL := "UPDATE statement_imports " +
		"SET status = 'completed', imported_count = $2, skipped_count = $3, error_count = 0, completed_at = now(), updated_at = now() " +
		"WHERE id = $1::uuid"
	_, err = tx.Exec(ctx, updateImportSQL, importID, importedCount, skippedCount)
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
func computeStatementDedupKey(accountID domain.AccountID, importID string, row usecasestmt.TransactionRow) string {
	ext := strings.TrimSpace(row.ExternalID)
	if ext != "" {
		sum := sha256.Sum256([]byte(fmt.Sprintf("v1|%s|ext|%s", accountID, ext)))
		return hex.EncodeToString(sum[:])
	}
	sum := sha256.Sum256([]byte(fmt.Sprintf("v1|%s|import|%s|line|%d|%s|%s|%s",
		accountID,
		importID,
		row.RowNumber,
		row.BookedAt.UTC().Format(time.RFC3339Nano),
		row.Value.Amount.String(),
		row.Value.Currency,
	)))
	return hex.EncodeToString(sum[:])
}
