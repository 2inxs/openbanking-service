package domain_test

import (
	"testing"

	"github.com/2inxs/openbanking-service/internal/domain"
)

func TestAccountMayHaveOnlyExternalIdentity(t *testing.T) {
	a := domain.Account{ID: "account", UserID: "user", BankID: "bank", ConnectionID: "connection",
		ExternalAccountID: "bank-account", Name: "Account", Type: domain.AccountTypeUnknown,
		Status: domain.AccountStatusUnknown, Currency: "RUB"}
	if err := a.Validate(); err != nil {
		t.Fatal(err)
	}
	a.Number = " "
	if err := a.Validate(); err == nil {
		t.Fatal("accepted whitespace as account number")
	}
	a.Number, a.ExternalAccountID = "", ""
	if err := a.Validate(); err == nil {
		t.Fatal("accepted account without identity")
	}
}
