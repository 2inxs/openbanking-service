// Пакет domain содержит бизнес-модели. После заполнения или изменения модель нужно валидировать.
package domain

import (
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/2inxs/openbanking-service/internal/domain/money"
)

// Локальные ID не совпадают с внешними идентификаторами, выданными банком.
type UserID string
type BankID string
type AccountID string
type BankConnectionID string
type BankCustomerID string
type TransactionID string
type StatementImportID string

var ErrInvalidModel = errors.New("invalid domain model")

func invalid(field, reason string) error {
	return fmt.Errorf("%w: %s %s", ErrInvalidModel, field, reason)
}

func required(field, value string) error {
	if value == "" || value != strings.TrimSpace(value) {
		return invalid(field, "must be nonempty and have no surrounding whitespace")
	}
	return nil
}

func optional(field, value string) error {
	if value == "" {
		return nil
	}
	return required(field, value)
}

type BankConnection struct {
	ID            BankConnectionID
	UserID        UserID
	BankID        BankID
	CustomerID    BankCustomerID // Может отсутствовать, если банк не выдаёт ID клиента.
	Status        ConnectionStatusID
	LastSyncedAt  *time.Time
	LastSyncError string // Только безопасное описание, без токенов и банковских реквизитов.
	Version       int64
}

func (c BankConnection) Validate() error {
	for _, field := range []struct{ name, value string }{
		{"connection.id", string(c.ID)},
		{"connection.user_id", string(c.UserID)},
		{"connection.bank_id", string(c.BankID)},
	} {
		if err := required(field.name, field.value); err != nil {
			return err
		}
	}
	if !c.Status.Valid() {
		return invalid("connection.status", "is unknown")
	}
	if err := optional("connection.customer_id", string(c.CustomerID)); err != nil {
		return err
	}
	if c.LastSyncedAt != nil && c.LastSyncedAt.IsZero() {
		return invalid("connection.last_synced_at", "must be absent or a real timestamp")
	}
	if c.Version < 0 {
		return invalid("connection.version", "must not be negative")
	}
	return nil
}

// BalanceSnapshot — остаток из банка или выписки на указанную дату.
// Суммы импортированных операций не прибавляются к этому остатку.
type BalanceSnapshot struct {
	Value money.Money
	AsOf  time.Time
}

type Account struct {
	ID                AccountID
	UserID            UserID
	BankID            BankID
	ConnectionID      BankConnectionID // Может отсутствовать у счёта из выписки.
	ExternalAccountID string
	Number            string
	Name              string
	Type              AccountTypeID
	Status            AccountStatusID
	Currency          money.Currency
	Balance           *BalanceSnapshot // nil — остаток неизвестен, а не равен нулю.
}

func (a Account) Validate() error {
	for _, field := range []struct{ name, value string }{
		{"account.id", string(a.ID)},
		{"account.user_id", string(a.UserID)},
		{"account.bank_id", string(a.BankID)},
		{"account.name", a.Name},
	} {
		if err := required(field.name, field.value); err != nil {
			return err
		}
	}
	if !a.Type.Valid() {
		return invalid("account.type", "is unknown")
	}
	if !a.Status.Valid() {
		return invalid("account.status", "is unknown")
	}
	if err := a.Currency.Validate(); err != nil {
		return fmt.Errorf("account.currency: %w", err)
	}
	if err := optional("account.connection_id", string(a.ConnectionID)); err != nil {
		return err
	}
	if err := optional("account.number", a.Number); err != nil {
		return err
	}
	if a.Number == "" && a.ExternalAccountID == "" {
		return invalid("account", "requires a number or external account ID")
	}
	if a.ExternalAccountID != "" {
		if a.ConnectionID == "" {
			return invalid("account.external_account_id", "requires a bank connection")
		}
		if err := required("account.external_account_id", a.ExternalAccountID); err != nil {
			return err
		}
	}
	if a.Balance != nil {
		if err := a.Balance.Value.Validate(); err != nil {
			return fmt.Errorf("account.balance: %w", err)
		}
		if a.Balance.Value.Currency != a.Currency {
			return fmt.Errorf("account.balance: %w", money.ErrCurrencyMismatch)
		}
		if a.Balance.AsOf.IsZero() {
			return invalid("account.balance.as_of", "is required for a known balance")
		}
	}
	return nil
}

func (a Account) ValidateForConnection(c BankConnection) error {
	if err := a.Validate(); err != nil {
		return err
	}
	if err := c.Validate(); err != nil {
		return err
	}
	if a.ConnectionID != c.ID || a.UserID != c.UserID || a.BankID != c.BankID {
		return invalid("account.connection", "must have the same ID, user and bank")
	}
	return nil
}
