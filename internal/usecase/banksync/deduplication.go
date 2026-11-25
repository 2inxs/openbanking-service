package banksync

import (
	"github.com/2inxs/openbanking-service/internal/domain"
)

// TransactionDeduplicationKey вычисляет детерминированный deduplication_key для операции,
// полученной из банковского API, по единому правилу приоритета внешнего ID (совместимому с выписками).
func TransactionDeduplicationKey(accountID domain.AccountID, tx BankTransaction) (string, domain.DeduplicationStrategy, error) {
	return domain.ComputeTransactionDeduplicationKey(domain.DeduplicationInput{
		AccountID:  accountID,
		Source:     domain.TransactionSourceOpenBanking,
		ExternalID: tx.ExternalID,
		Amount:     tx.Value.Amount,
		Currency:   tx.Value.Currency,
		BookedAt:   tx.BookedAt,
		ValueAt:    tx.ValueAt,
	})
}
