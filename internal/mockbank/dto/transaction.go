package dto

import (
	"time"

	"github.com/2inxs/openbanking-service/internal/mockbank/model"
)

// TransactionsResponse — ответ на GET /accounts/{accountId}/transactions.
type TransactionsResponse struct {
	Data  TransactionsData `json:"Data"`
	Links PageLinks        `json:"Links"`
	Meta  Meta             `json:"Meta"`
}

// TransactionsData — содержимое Data: список операций.
type TransactionsData struct {
	Transaction []TransactionDTO `json:"Transaction"`
}

// TransactionDTO — одна операция в формате стандарта.
type TransactionDTO struct {
	AccountID              string              `json:"accountId"`
	TransactionID          string              `json:"transactionId"`
	CreditDebitIndicator   string              `json:"creditDebitIndicator"`
	Status                 string              `json:"status"`
	BookingDateTime        string              `json:"bookingDateTime"`
	ValueDateTime          string              `json:"valueDateTime,omitempty"`
	TransactionInformation string              `json:"transactionInformation,omitempty"`
	Amount                 AmountDTO           `json:"Amount"`
	MerchantDetails        *MerchantDetailsDTO `json:"MerchantDetails,omitempty"`
	CardInstrument         *CardInstrumentDTO  `json:"CardInstrument,omitempty"`
}

// MerchantDetailsDTO — сведения о магазине.
type MerchantDetailsDTO struct {
	MerchantName         string `json:"merchantName,omitempty"`
	MerchantCategoryCode string `json:"merchantCategoryCode,omitempty"`
}

// CardInstrumentDTO — карта, которой оплачена операция.
type CardInstrumentDTO struct {
	CardSchemeName string `json:"cardSchemeName"`
	Name           string `json:"name,omitempty"`
	Identification string `json:"identification,omitempty"`
}

// PageLinks — ссылки для постраничной выдачи. self обязателен, остальные — когда страниц больше одной.
type PageLinks struct {
	Self  string `json:"self"`
	First string `json:"first,omitempty"`
	Prev  string `json:"prev,omitempty"`
	Next  string `json:"next,omitempty"`
	Last  string `json:"last,omitempty"`
}

// ToTransactionDTO переводит операцию в формат стандарта.
// acc нужен для валюты и карты: операции хранятся в валюте своего счёта,
// а оплаченная картой операция ссылается на карту этого счёта.
func ToTransactionDTO(tx model.Transaction, acc model.Account) TransactionDTO {
	indicator := IndicatorCredit
	if tx.Direction == model.Debit {
		indicator = IndicatorDebit
	}

	out := TransactionDTO{
		AccountID:              tx.AccountID,
		TransactionID:          tx.ID,
		CreditDebitIndicator:   indicator,
		Status:                 string(tx.Status),
		BookingDateTime:        tx.BookedAt.Format(time.RFC3339),
		TransactionInformation: tx.Description,
		Amount:                 AmountDTO{Amount: FormatAmount(tx.Amount), Currency: acc.Currency},
	}
	if !tx.ValueAt.IsZero() {
		out.ValueDateTime = tx.ValueAt.Format(time.RFC3339)
	}
	if tx.MerchantName != "" || tx.MCC != "" {
		out.MerchantDetails = &MerchantDetailsDTO{
			MerchantName:         tx.MerchantName,
			MerchantCategoryCode: tx.MCC,
		}
	}
	if tx.ByCard && acc.Card != nil {
		out.CardInstrument = &CardInstrumentDTO{
			CardSchemeName: acc.Card.Scheme,
			Name:           acc.Card.HolderName,
			Identification: acc.Card.MaskedNumber,
		}
	}
	return out
}
