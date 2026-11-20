package dto

import (
	"time"

	"github.com/2inxs/openbanking-service/internal/mockbank/model"
)

const balanceTypeClosingAvailable = "ClosingAvailable"

type BalancesResponse struct {
	Data  BalancesData `json:"Data"`
	Links Links        `json:"Links"`
	Meta  Meta         `json:"Meta"`
}

type BalancesData struct {
	Balance []BalanceDTO `json:"Balance"`
}

type BalanceDTO struct {
	AccountID            string    `json:"accountId"`
	CreditDebitIndicator string    `json:"creditDebitIndicator"`
	Type                 string    `json:"type"`
	DateTime             string    `json:"dateTime"`
	Amount               AmountDTO `json:"Amount"`
}

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
