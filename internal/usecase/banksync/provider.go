package banksync

import (
	"context"
	"time"

	"github.com/2inxs/openbanking-service/internal/domain"
	"github.com/2inxs/openbanking-service/internal/domain/money"
)

// BankAccount - счет из банка в формате приложения, без локальных ID
type BankAccount struct {
	ExternalID string // accountId банка
	Number     string
	Name       string
	Type       domain.AccountTypeID
	Status     domain.AccountStatusID
	Currency   money.Currency
}

// BankTransaction - операция из банка в формате приложения, без локальных ID
type BankTransaction struct {
	ExternalID   string // transactionId банка
	Status       domain.TransactionStatusID
	RawStatus    string
	Value        money.Money // со знаком: поступление +, списание −
	BookedAt     *time.Time  // nil — у Pending даты может не быть
	ValueAt      *time.Time
	Description  string
	MerchantName string
	MCC          string
}

// BankClient - то, что синхронизации нужно от банка
type BankClient interface {
	Accounts(ctx context.Context) ([]BankAccount, error)
	Balance(ctx context.Context, accountID string) (domain.BalanceSnapshot, error)
	Transactions(ctx context.Context, accountID string, from, to time.Time) ([]BankTransaction, error)
}
