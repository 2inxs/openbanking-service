package domain

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/2inxs/openbanking-service/internal/domain/money"
)

const (
	maxDedupAccountIDLen  = 128
	maxDedupExternalIDLen = 255 // соответствует varchar(255) в transactions.external_transaction_id
	maxDedupImportIDLen   = 128
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

func validateBoundedToken(val string, maxRunes int, allowInternalSpaces bool) error {
	if val == "" || !utf8.ValidString(val) || utf8.RuneCountInString(val) > maxRunes || val != strings.TrimSpace(val) {
		return ErrInvalidModel
	}
	for i := 0; i < len(val); i++ {
		b := val[i]
		if b < 0x20 || b == 0x7f || (!allowInternalSpaces && b == 0x20) {
			return ErrInvalidModel
		}
	}
	return nil
}

// ComputeTransactionDeduplicationKey вычисляет канонический 64-символьный hex SHA-256 ключ
// для уникального индекса (account_id, deduplication_key) в таблице transactions.
func ComputeTransactionDeduplicationKey(in DeduplicationInput) (string, DeduplicationStrategy, error) {
	accID := string(in.AccountID)
	if err := validateBoundedToken(accID, maxDedupAccountIDLen, false); err != nil {
		return "", "", fmt.Errorf("%w: invalid account_id", ErrInvalidModel)
	}
	if !in.Source.Valid() {
		return "", "", fmt.Errorf("%w: transaction source is unknown", ErrInvalidModel)
	}
	if err := (money.Money{Amount: in.Amount, Currency: in.Currency}).Validate(); err != nil {
		return "", "", fmt.Errorf("%w: %v", ErrInvalidModel, err)
	}

	refTime, hasTime := effectiveTransactionTime(in.BookedAt, in.ValueAt)
	if !hasTime {
		return "", "", fmt.Errorf("%w: booked_at or value_at is required", ErrInvalidModel)
	}

	extID := strings.TrimSpace(in.ExternalID)
	if extID != "" {
		if err := validateBoundedToken(extID, maxDedupExternalIDLen, true); err != nil {
			return "", "", fmt.Errorf("%w: invalid external_id", ErrInvalidModel)
		}
		// Кодирование с явной байтовой длиной полей исключает коллизии при наличии символа '|' внутри external_id.
		payload := fmt.Sprintf("v1|acc:%d:%s|ext:%d:%s", len(accID), accID, len(extID), extID)
		sum := sha256.Sum256([]byte(payload))
		return hex.EncodeToString(sum[:]), DedupStrategyExternalID, nil
	}

	// При отсутствии external_id одинаковые сумма и дата сами по себе не считаются дубликатом:
	// для выписки используется позиционный ключ строки импорта, чтобы сохранить два реальных
	// одинаковых платежа (например, две покупки на одну сумму в один день).
	if in.Source == SourceStatement {
		impID := string(in.StatementImportID)
		if err := validateBoundedToken(impID, maxDedupImportIDLen, false); err != nil || in.RowNumber <= 0 {
			return "", "", fmt.Errorf("%w: valid statement import id and positive row number are required without external_id", ErrInsufficientDeduplicationData)
		}
		amtStr := in.Amount.String()
		curStr := string(in.Currency)
		timeStr := refTime.UTC().Format(time.RFC3339Nano)
		payload := fmt.Sprintf(
			"v1|acc:%d:%s|imp:%d:%s|line:%d|at:%d:%s|amt:%d:%s|cur:%d:%s",
			len(accID), accID,
			len(impID), impID,
			in.RowNumber,
			len(timeStr), timeStr,
			len(amtStr), amtStr,
			len(curStr), curStr,
		)
		sum := sha256.Sum256([]byte(payload))
		return hex.EncodeToString(sum[:]), DedupStrategyStatementLine, nil
	}

	return "", "", fmt.Errorf("%w: external_id is required for source %d", ErrInsufficientDeduplicationData, in.Source)
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
