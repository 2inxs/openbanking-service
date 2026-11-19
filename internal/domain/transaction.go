package domain

import (
	"fmt"
	"time"

	"github.com/2inxs/openbanking-service/internal/domain/money"
)

// Поступление положительное, списание отрицательное.
// Pending и Rejected не учитываются в доходах и расходах проведённых операций.
type Transaction struct {
	ID                    TransactionID
	AccountID             AccountID
	StatementImportID     StatementImportID
	ExternalTransactionID string // ID банка; может отсутствовать у операции из выписки.
	Source                TransactionSourceID
	Status                TransactionStatusID
	Value                 money.Money // В валюте счёта; исходная сумма покупки — в OriginalValue.
	OriginalValue         *money.Money
	BookedAt              *time.Time
	ValueAt               *time.Time
	Description           string
	CounterpartyName      string
	CounterpartyAccount   string
	MerchantName          string
	MCC                   string
}

func (t Transaction) Validate() error {
	if err := required("transaction.id", string(t.ID)); err != nil {
		return err
	}
	if err := required("transaction.account_id", string(t.AccountID)); err != nil {
		return err
	}
	if !t.Source.Valid() {
		return invalid("transaction.source", "is unknown")
	}
	if !t.Status.Valid() {
		return invalid("transaction.status", "is unknown")
	}
	if t.Source == SourceStatement && t.StatementImportID == "" {
		return invalid("transaction.statement_import_id", "is required for an imported transaction")
	}
	if err := optional("transaction.statement_import_id", string(t.StatementImportID)); err != nil {
		return err
	}
	if t.ExternalTransactionID != "" {
		if err := required("transaction.external_transaction_id", t.ExternalTransactionID); err != nil {
			return err
		}
	}
	if err := t.Value.Validate(); err != nil {
		return fmt.Errorf("transaction.value: %w", err)
	}
	if t.Status == TransactionBooked && t.BookedAt == nil {
		return invalid("transaction.booked_at", "is required for a booked transaction")
	}
	for _, timestamp := range []*time.Time{t.BookedAt, t.ValueAt} {
		if timestamp != nil && timestamp.IsZero() {
			return invalid("transaction.timestamp", "must be absent or a real timestamp")
		}
	}
	if t.OriginalValue != nil {
		if err := t.OriginalValue.Validate(); err != nil {
			return fmt.Errorf("transaction.original_value: %w", err)
		}
		if t.Value.Amount.Sign()*t.OriginalValue.Amount.Sign() < 0 {
			return invalid("transaction.original_value", "must have the same direction as the account movement")
		}
	}
	if t.MCC != "" {
		if len(t.MCC) != 4 {
			return invalid("transaction.mcc", "must contain four digits")
		}
		for _, c := range t.MCC {
			if c < '0' || c > '9' {
				return invalid("transaction.mcc", "must contain four digits")
			}
		}
	}
	return nil
}

func (t Transaction) ValidateForAccount(a Account) error {
	if err := t.Validate(); err != nil {
		return err
	}
	if err := a.Validate(); err != nil {
		return err
	}
	if t.AccountID != a.ID {
		return invalid("transaction.account_id", "does not match the account")
	}
	if t.Value.Currency != a.Currency {
		return fmt.Errorf("transaction.account_currency: %w", money.ErrCurrencyMismatch)
	}
	return nil
}
