package domain_test

import (
	"errors"
	"testing"
	"time"

	"github.com/2inxs/openbanking-service/internal/domain"
	"github.com/2inxs/openbanking-service/internal/domain/money"
)

func value(t *testing.T, amount, currency string) money.Money {
	t.Helper()
	a, err := money.Parse(amount)
	if err != nil {
		t.Fatal(err)
	}
	m, err := money.New(a, money.Currency(currency))
	if err != nil {
		t.Fatal(err)
	}
	return m
}

func account() domain.Account {
	return domain.Account{ID: "account-1", UserID: "user-1", BankID: "bank-1", Number: "40817810000000000001", Name: "Основной", Currency: money.RUB}
}

func connection() domain.BankConnection {
	return domain.BankConnection{ID: "connection-1", UserID: "user-1", BankID: "bank-1", Status: domain.ConnectionPending}
}

func transaction(t *testing.T) domain.Transaction {
	return domain.Transaction{ID: "transaction-1", AccountID: "account-1", Source: domain.SourceOpenBanking,
		Status: domain.TransactionPending, Value: value(t, "-100.25", "RUB")}
}

func TestAccountBalanceIsOptionalAndMayBeNegative(t *testing.T) {
	a := account()
	if err := a.Validate(); err != nil {
		t.Fatal(err)
	}
	for _, amount := range []string{"0", "-100.25", "1250.50"} {
		a.Balance = &domain.BalanceSnapshot{Value: value(t, amount, "RUB"), AsOf: time.Now()}
		if err := a.Validate(); err != nil {
			t.Fatalf("valid balance %s rejected: %v", amount, err)
		}
	}
	a.Balance = &domain.BalanceSnapshot{Value: value(t, "10", "EUR"), AsOf: time.Now()}
	if err := a.Validate(); !errors.Is(err, money.ErrCurrencyMismatch) {
		t.Fatalf("accepted wrong balance currency: %v", err)
	}
	a.Balance = &domain.BalanceSnapshot{Value: value(t, "10", "RUB")}
	if err := a.Validate(); !errors.Is(err, domain.ErrInvalidModel) {
		t.Fatalf("accepted missing balance timestamp: %v", err)
	}
}

func TestConnectionBelongsToSameUserAndBank(t *testing.T) {
	c := connection()
	a := account()
	a.ConnectionID = c.ID
	a.ExternalAccountID = "external-account-1"
	if err := a.ValidateForConnection(c); err != nil {
		t.Fatal(err)
	}
	for _, mutate := range []func(*domain.BankConnection){
		func(c *domain.BankConnection) { c.ID = "connection-2" },
		func(c *domain.BankConnection) { c.UserID = "user-2" },
		func(c *domain.BankConnection) { c.BankID = "bank-2" },
	} {
		other := c
		mutate(&other)
		if err := a.ValidateForConnection(other); !errors.Is(err, domain.ErrInvalidModel) {
			t.Fatalf("accepted mismatched connection: %v", err)
		}
	}
	a.ConnectionID = ""
	if err := a.Validate(); !errors.Is(err, domain.ErrInvalidModel) {
		t.Fatalf("external account has no connection: %v", err)
	}
}

func TestConnectionValidation(t *testing.T) {
	for _, mutate := range []func(*domain.BankConnection){
		func(c *domain.BankConnection) { c.ID = "" },
		func(c *domain.BankConnection) { c.UserID = "" },
		func(c *domain.BankConnection) { c.BankID = "" },
		func(c *domain.BankConnection) { c.CustomerID = " " },
		func(c *domain.BankConnection) { c.Status = 99 },
		func(c *domain.BankConnection) { c.LastSyncedAt = new(time.Time) },
	} {
		c := connection()
		mutate(&c)
		if err := c.Validate(); !errors.Is(err, domain.ErrInvalidModel) {
			t.Fatalf("expected invalid model, got %v", err)
		}
	}
}

func TestAccountRejectsMissingIdentityAndCurrency(t *testing.T) {
	for _, mutate := range []func(*domain.Account){
		func(a *domain.Account) { a.ID = "" },
		func(a *domain.Account) { a.UserID = "" },
		func(a *domain.Account) { a.BankID = "" },
		func(a *domain.Account) { a.Number = " " },
		func(a *domain.Account) { a.Name = "" },
		func(a *domain.Account) { a.Currency = "rub" },
	} {
		a := account()
		mutate(&a)
		if err := a.Validate(); err == nil {
			t.Fatalf("accepted invalid account: %+v", a)
		}
	}
}

func TestPendingAndBookedTransactions(t *testing.T) {
	tx := transaction(t)
	if err := tx.ValidateForAccount(account()); err != nil {
		t.Fatal(err)
	}
	tx.Status = domain.TransactionBooked
	if err := tx.Validate(); !errors.Is(err, domain.ErrInvalidModel) {
		t.Fatalf("booked transaction has no date: %v", err)
	}
	bookedAt := time.Date(2026, 11, 19, 12, 0, 0, 0, time.UTC)
	tx.BookedAt = &bookedAt
	if err := tx.ValidateForAccount(account()); err != nil {
		t.Fatal(err)
	}
	other := account()
	other.ID = "account-2"
	if err := tx.ValidateForAccount(other); !errors.Is(err, domain.ErrInvalidModel) {
		t.Fatalf("accepted another account: %v", err)
	}
	tx.Value = value(t, "-1", "EUR")
	if err := tx.ValidateForAccount(account()); !errors.Is(err, money.ErrCurrencyMismatch) {
		t.Fatalf("accepted another currency: %v", err)
	}
}

func TestTransactionOriginalCurrencyAndSource(t *testing.T) {
	tx := transaction(t)
	original := value(t, "-1.10", "EUR")
	tx.OriginalValue = &original
	tx.MCC = "5411"
	if err := tx.Validate(); err != nil {
		t.Fatal(err)
	}
	original = value(t, "1.10", "EUR")
	if err := tx.Validate(); !errors.Is(err, domain.ErrInvalidModel) {
		t.Fatalf("accepted opposite original direction: %v", err)
	}
	tx.OriginalValue = nil
	tx.Source = domain.SourceStatement
	if err := tx.Validate(); !errors.Is(err, domain.ErrInvalidModel) {
		t.Fatalf("import has no source record: %v", err)
	}
	tx.StatementImportID = "import-1"
	if err := tx.Validate(); err != nil {
		t.Fatal(err)
	}
}

func TestTransactionOriginalDirectionIncludingZero(t *testing.T) {
	for _, tc := range []struct {
		name     string
		movement string
		original string
		valid    bool
	}{
		{"credit", "100", "1", true},
		{"debit", "-100", "-1", true},
		{"credit with original debit", "100", "-1", false},
		{"debit with original credit", "-100", "1", false},
		{"zero with original credit", "0", "1", false},
		{"zero with original debit", "0", "-1", false},
		{"both zero", "0", "0", true},
		{"credit with zero original", "100", "0", true},
		{"debit with zero original", "-100", "0", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			tx := transaction(t)
			tx.Value = value(t, tc.movement, "RUB")
			original := value(t, tc.original, "EUR")
			tx.OriginalValue = &original
			err := tx.Validate()
			if tc.valid && err != nil {
				t.Fatalf("valid original direction rejected: %v", err)
			}
			if !tc.valid && !errors.Is(err, domain.ErrInvalidModel) {
				t.Fatalf("expected invalid original direction, got %v", err)
			}
		})
	}
}

func TestTransactionRejectsInvalidFields(t *testing.T) {
	for _, mutate := range []func(*domain.Transaction){
		func(tx *domain.Transaction) { tx.ID = "" },
		func(tx *domain.Transaction) { tx.AccountID = "" },
		func(tx *domain.Transaction) { tx.Status = 0 },
		func(tx *domain.Transaction) { tx.Source = 99 },
		func(tx *domain.Transaction) { tx.ExternalTransactionID = " " },
		func(tx *domain.Transaction) { tx.StatementImportID = " " },
		func(tx *domain.Transaction) { tx.Value.Currency = "" },
		func(tx *domain.Transaction) { tx.MCC = "abc1" },
		func(tx *domain.Transaction) { tx.MCC = "12345" },
		func(tx *domain.Transaction) { tx.ValueAt = new(time.Time) },
	} {
		tx := transaction(t)
		mutate(&tx)
		if err := tx.Validate(); err == nil {
			t.Fatalf("accepted invalid transaction: %+v", tx)
		}
	}
}

func TestStatusIDsRemainStable(t *testing.T) {
	if domain.ConnectionActive != 1 || domain.ConnectionError != 2 || domain.ConnectionDisconnected != 3 || domain.ConnectionPending != 4 {
		t.Fatal("connection status IDs changed")
	}
	if domain.TransactionPending != 1 || domain.TransactionBooked != 2 || domain.TransactionRejected != 3 || domain.SourceOpenBanking != 1 || domain.SourceStatement != 2 {
		t.Fatal("transaction status/source IDs changed")
	}
	for _, status := range []domain.ConnectionStatusID{-1, 0, 99} {
		if status.Valid() {
			t.Fatalf("unknown connection status %d accepted", status)
		}
	}
	for _, status := range []domain.TransactionStatusID{-1, 0, 99} {
		if status.Valid() {
			t.Fatalf("unknown transaction status %d accepted", status)
		}
	}
	for _, source := range []domain.TransactionSourceID{-1, 0, 99} {
		if source.Valid() {
			t.Fatalf("unknown source %d accepted", source)
		}
	}
}
