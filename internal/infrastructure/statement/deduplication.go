package statement

import (
	"github.com/2inxs/openbanking-service/internal/domain"
	usecasestmt "github.com/2inxs/openbanking-service/internal/usecase/statement"
)

// RowDeduplicationKey вычисляет deduplication_key для строки выписки:
// при наличии ExternalID формируется общий с Open Banking API ключ,
// при отсутствии ExternalID — позиционный ключ строки импорта без потери одинаковых платежей.
func RowDeduplicationKey(
	accountID domain.AccountID,
	importID domain.StatementImportID,
	row usecasestmt.TransactionRow,
) (string, domain.DeduplicationStrategy, error) {
	booked := row.BookedAt
	return domain.ComputeTransactionDeduplicationKey(domain.DeduplicationInput{
		AccountID:         accountID,
		Source:            domain.SourceStatement,
		ExternalID:        row.ExternalID,
		Amount:            row.Value.Amount,
		Currency:          row.Value.Currency,
		BookedAt:          &booked,
		StatementImportID: importID,
		RowNumber:         row.RowNumber,
	})
}
