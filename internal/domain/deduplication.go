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

var ErrInsufficientDeduplicationData = errors.New("transaction: insufficient data for reliable deduplication")

type DeduplicationStrategy string

const (
	DedupStrategyExternalID    DeduplicationStrategy = "external_id"
	DedupStrategyStatementLine DeduplicationStrategy = "statement_line"
)

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

func ComputeTransactionDeduplicationKey(in DeduplicationInput) (string, DeduplicationStrategy, error) {
	accID := strings.TrimSpace(string(in.AccountID))
	if accID == "" || accID != string(in.AccountID) {
		return "", "", fmt.Errorf("%w: account_id is required", ErrInvalidModel)
	}
	if !in.Source.Valid() {
		return "", "", fmt.Errorf("%w: transaction source is unknown", ErrInvalidModel)
	}
	if err := (money.Money{Amount: in.Amount, Currency: in.Currency}).Validate(); err != nil {
		return "", "", fmt.Errorf("%w: %v", ErrInvalidModel, err)
	}

	extID := strings.TrimSpace(in.ExternalID)
	if extID != "" {
		sum := sha256.Sum256([]byte(fmt.Sprintf("v1|%s|ext|%s", accID, extID)))
		return hex.EncodeToString(sum[:]), DedupStrategyExternalID, nil
	}

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
