package dto

import (
	"fmt"

	"github.com/2inxs/openbanking-service/internal/mockbank/model"
)

const (
	accountTypePersonal    = "Personal"
	schemeCBRAccountNumber = "RU.CBR.AccountNumber"
)

type AccountsResponse struct {
	Data  AccountsData `json:"Data"`
	Links Links        `json:"Links"`
	Meta  Meta         `json:"Meta"`
}

type AccountsData struct {
	Account []AccountDTO `json:"Account"`
}

type AccountDTO struct {
	AccountID          string              `json:"accountId"`
	Status             string              `json:"status,omitempty"`
	Currency           string              `json:"currency"`
	AccountType        string              `json:"accountType"`
	AccountSubType     string              `json:"accountSubType"`
	AccountDescription string              `json:"accountDescription,omitempty"`
	AccountDetails     []AccountDetailsDTO `json:"AccountDetails,omitempty"`
}

type AccountDetailsDTO struct {
	SchemeName     string `json:"schemeName"`
	Identification string `json:"identification"`
	Name           string `json:"name,omitempty"`
}

type Links struct {
	Self string `json:"self"`
}

type Meta struct {
	TotalPages int `json:"totalPages,omitempty"`
}

func ToAccountDTO(acc model.Account) AccountDTO {
	return AccountDTO{
		AccountID:          acc.ID,
		Status:             string(acc.Status),
		Currency:           acc.Currency,
		AccountType:        accountTypePersonal,
		AccountSubType:     string(acc.SubType),
		AccountDescription: acc.Description,
		AccountDetails: []AccountDetailsDTO{
			{
				SchemeName:     schemeCBRAccountNumber,
				Identification: acc.Number,
				Name:           acc.Description,
			},
		},
	}
}

func formatAmount(minor int64) string {
	units := minor / 100
	kop := minor % 100
	return fmt.Sprintf("%d.%02d", units, kop)
}
