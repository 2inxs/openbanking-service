package dto

import (
	"time"

	"github.com/2inxs/openbanking-service/internal/mockbank/model"
)

// Тип баланса по стандарту: доступный остаток на момент ответа.
const balanceTypeClosingAvailable = "ClosingAvailable"

// BalancesResponse — ответ на GET /accounts/{accountId}/balances.
type BalancesResponse struct {
	Data  BalancesData `json:"Data"`
	Links Links        `json:"Links"`
	Meta  Meta         `json:"Meta"`
}

// BalancesData — содержимое Data: список балансов.
type BalancesData struct {
	Balance []BalanceDTO `json:"Balance"`
}

// BalanceDTO — один баланс счёта в формате стандарта.
type BalanceDTO struct {
	AccountID            string    `json:"accountId"`
	CreditDebitIndicator string    `json:"creditDebitIndicator"`
	Type                 string    `json:"type"`
	DateTime             string    `json:"dateTime"`
	Amount               AmountDTO `json:"Amount"`
}

// ToBalanceDTO переводит баланс счёта в формат стандарта.
// Отрицательный баланс (например, кредитная карта в минусе) отдаётся
// модулем суммы с creditDebitIndicator = Debit.
func ToBalanceDTO(acc model.Account, at time.Time) BalanceDTO {
	amount, indicator := ToSignedAmount(acc.BalanceKop, acc.Currency)
	return BalanceDTO{
		AccountID:            acc.ID,
		CreditDebitIndicator: indicator,
		Type:                 balanceTypeClosingAvailable,
		DateTime:             at.Format(time.RFC3339),
		Amount:               amount,
	}
}
