package domain

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/2inxs/openbanking-service/internal/domain/money"
)

// ErrInsufficientDeduplicationData возвращается, когда у операции недостаточно
// надёжных идентификаторов для безопасного сопоставления без риска ложного слияния.
var ErrInsufficientDeduplicationData = errors.New("transaction: insufficient data for reliable deduplication")

// DeduplicationStrategy описывает правило, по которому был сформирован deduplication_key.
type DeduplicationStrategy string

const (
	// DedupStrategyExternalID — детерминированный ключ по стабильному внешнему ID банка (приоритет 1).
	// Совпадает для одного и того же платежа, пришедшего из Open Banking API и из выписки.
	DedupStrategyExternalID DeduplicationStrategy = "external_id"

	// DedupStrategyStatementLine — позиционный ключ строки выписки при отсутствии external_id (приоритет 2).
	// Сохраняет два реальных одинаковых платежа внутри выписки, не удаляя их молча.
	DedupStrategyStatementLine DeduplicationStrategy = "statement_line"
)

// DeduplicationInput содержит нормализованные атрибуты операции для вычисления deduplication_key.
type DeduplicationInput struct {
	AccountID         AccountID
	Source            TransactionSourceID
	ExternalID        string
	Amount            money.Amount
	Currency          money.Currency
	BookedAt          *time.Time
	ValueAt           *time.Time
	StatementImportID StatementImportID
	RowNumber         int
}

// ComputeTransactionDeduplicationKey вычисляет канонический 64-символьный hex SHA-256 ключ
// для уникального индекса (account_id, deduplication_key) в таблице transactions.
func ComputeTransactionDeduplicationKey(in DeduplicationInput) (string, DeduplicationStrategy, error) {
	accID := strings.TrimSpace(string(in.AccountID))
	if accID == "" || accID != string(in.AccountID) {
		return "", "", fmt.Errorf("%w: account_id is required", ErrInvalidModel)
	}
	if err := in.Amount.Validate(); err != nil {
		return "", "", fmt.Errorf("%w: %v", ErrInvalidModel, err)
	}
	if err := in.Currency.Validate(); err != nil {
		return "", "", fmt.Errorf("%w: %v", ErrInvalidModel, err)
	}

	extID := strings.TrimSpace(in.ExternalID)
	if extID != "" {
		sum := sha256.Sum256([]byte(fmt.Sprintf("v1|%s|ext|%s", accID, extID)))
		return hex.EncodeToString(sum[:]), DedupStrategyExternalID, nil
	}

	// При отсутствии external_id одинаковые сумма и дата сами по себе не считаются дубликатом:
	// для выписки используется позиционный ключ строки импорта, чтобы сохранить два реальных
	// одинаковых платежа (например, две покупки на одну сумму в один день).
	if in.Source == SourceStatement {
		impID := strings.TrimSpace(string(in.StatementImportID))
		if impID == "" || impID != string(in.StatementImportID) || in.RowNumber <= 0 {
			return "", "", fmt.Errorf("%w: statement import id and positive row number are required without external_id", ErrInsufficientDeduplicationData)
		}
		refTime, ok := effectiveTransactionTime(in.BookedAt, in.ValueAt)
		if !ok {
			return "", "", fmt.Errorf("%w: booked_at or value_at is required", ErrInvalidModel)
		}
		sum := sha256.Sum256([]byte(fmt.Sprintf(
			"v1|%s|import|%s|line|%d|%s|%s|%s",
			accID,
			impID,
			in.RowNumber,
			refTime.UTC().Format(time.RFC3339Nano),
			in.Amount.String(),
			in.Currency,
		)))
		return hex.EncodeToString(sum[:]), DedupStrategyStatementLine, nil
	}

	return "", "", fmt.Errorf("%w: external_id is required for source %q", ErrInsufficientDeduplicationData, in.Source)
}

func effectiveTransactionTime(bookedAt, valueAt *time.Time) (time.Time, bool) {
	if bookedAt != nil && !bookedAt.IsZero() {
		return bookedAt.UTC(), true
	}
	if valueAt != nil && !valueAt.IsZero() {
		return valueAt.UTC(), true
	}
	return time.Time{}, false
}
