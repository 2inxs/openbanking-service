package openbanking

import (
	"errors"
	"os"
	"testing"

	"github.com/2inxs/openbanking-service/internal/domain"
	"github.com/2inxs/openbanking-service/internal/domain/money"
	"github.com/2inxs/openbanking-service/internal/usecase/banksync"
)

func readFixture(t *testing.T, name string) []byte {
	t.Helper()
	data, err := os.ReadFile("testdata/" + name)
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}
	return data
}

func assertInvalid(t *testing.T, err error) {
	t.Helper()
	if !errors.Is(err, ErrInvalidBankData) {
		t.Fatalf("got %v, want ErrInvalidBankData", err)
	}
}

// ---------- Счета ----------

func TestParseAccounts(t *testing.T) {
	accounts, err := ParseAccounts(readFixture(t, "accounts.json"))
	if err != nil {
		t.Fatal(err)
	}
	if len(accounts) != 3 {
		t.Fatalf("got %d accounts, want 3", len(accounts))
	}

	byID := make(map[string]banksync.BankAccount)
	for _, acc := range accounts {
		byID[acc.ExternalID] = acc
		if acc.Number == "" || acc.Name == "" || acc.Status != domain.AccountActive {
			t.Errorf("%s: %+v", acc.ExternalID, acc)
		}
	}
	if byID["acc-002"].Type != domain.AccountCreditCard {
		t.Errorf("acc-002 type %d, want CreditCard", byID["acc-002"].Type)
	}
	if usd := byID["acc-003"]; usd.Currency != "USD" || usd.Type != domain.AccountSavings {
		t.Errorf("acc-003: %+v", usd)
	}
}

func validBankAccount() bankAccount {
	return bankAccount{
		AccountID:          "acc-1",
		Status:             "Enabled",
		Currency:           "RUB",
		AccountSubType:     "CurrentAccount",
		AccountDescription: "Основной",
		AccountDetails: []bankAccountDetails{
			{SchemeName: schemeCBRAccountNumber, Identification: "40817810000000000001"},
		},
	}
}

func TestNormalizeAccountErrors(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*bankAccount)
	}{
		{"empty id", func(a *bankAccount) { a.AccountID = "" }},
		{"bad currency", func(a *bankAccount) { a.Currency = "rub" }},
		{"no details", func(a *bankAccount) { a.AccountDetails = nil }},
		{"only other scheme", func(a *bankAccount) {
			a.AccountDetails = []bankAccountDetails{{SchemeName: "RU.CBR.PAN", Identification: "4276"}}
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			acc := validBankAccount()
			tt.mutate(&acc)
			_, err := normalizeAccount(acc)
			assertInvalid(t, err)
		})
	}
}

func TestNormalizeAccountDefaults(t *testing.T) {
	acc := validBankAccount()
	acc.AccountDescription = ""
	acc.Status = "Disabled"
	acc.AccountSubType = "Loan"

	got, err := normalizeAccount(acc)
	if err != nil {
		t.Fatal(err)
	}
	if got.Name != got.Number {
		t.Errorf("empty description: name %q, want number", got.Name)
	}
	if got.Status != domain.AccountStatusUnknown || got.Type != domain.AccountTypeUnknown {
		t.Errorf("status %d, type %d, want unknown", got.Status, got.Type)
	}
}

// ---------- Сумма ----------

func TestSignedMoney(t *testing.T) {
	tests := []struct {
		amount, indicator, currency string
		want                        string
	}{
		{"350.00", "Credit", "RUB", "350.0000"},
		{"350.00", "Debit", "RUB", "-350.0000"},
		{"-350.00", "Debit", "RUB", ""},
		{"abc", "Credit", "RUB", ""},
		{"350.00", "Debet", "RUB", ""},
		{"350.00", "Credit", "rub", ""},
	}
	for _, tt := range tests {
		t.Run(tt.amount+" "+tt.indicator+" "+tt.currency, func(t *testing.T) {
			m, err := signedMoney(bankAmount{Amount: tt.amount, Currency: tt.currency}, tt.indicator)
			if tt.want == "" {
				if err == nil {
					t.Fatalf("got %s, want error", m.Amount)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if m.Amount.String() != tt.want || m.Currency != money.Currency(tt.currency) {
				t.Errorf("got %s %s, want %s %s", m.Amount, m.Currency, tt.want, tt.currency)
			}
		})
	}
}

// ---------- Баланс ----------

func TestParseBalance(t *testing.T) {
	data := readFixture(t, "balances_acc-002.json")

	b, err := ParseBalance(data, "acc-002")
	if err != nil {
		t.Fatal(err)
	}
	if b.Value.Amount.String() != "-17190.0000" || b.Value.Currency != money.RUB || b.AsOf.IsZero() {
		t.Errorf("got %s %s at %v", b.Value.Amount, b.Value.Currency, b.AsOf)
	}

	t.Run("other account", func(t *testing.T) {
		_, err := ParseBalance(data, "acc-001")
		assertInvalid(t, err)
	})
	t.Run("no ClosingAvailable", func(t *testing.T) {
		_, err := ParseBalance([]byte(`{"Data":{"Balance":[]}}`), "acc-002")
		assertInvalid(t, err)
	})
}

// ---------- Операции ----------

func TestParseTransactions(t *testing.T) {
	txs, err := ParseTransactions(readFixture(t, "transactions_acc-001.json"), "acc-001")
	if err != nil {
		t.Fatal(err)
	}
	if len(txs) != 6 {
		t.Fatalf("got %d transactions, want 6", len(txs))
	}

	byID := make(map[string]banksync.BankTransaction)
	for _, tx := range txs {
		byID[tx.ExternalID] = tx
	}

	salary := byID["tx-0001"]
	if salary.Value.Amount.String() != "80000.0000" || salary.Status != domain.TransactionBooked || salary.MerchantName != "" {
		t.Errorf("salary: %+v", salary)
	}
	coffee := byID["tx-0004"]
	if coffee.Value.Amount.String() != "-350.0000" || coffee.MCC != "5814" || coffee.BookedAt == nil {
		t.Errorf("coffee: %+v", coffee)
	}
	if byID["tx-0006"].Status != domain.TransactionPending {
		t.Errorf("tx-0006 status %d, want Pending", byID["tx-0006"].Status)
	}
	if _, ok := byID["tx-0005"]; !ok {
		t.Error("second identical coffee tx-0005 is missing")
	}
}

func validBankTransaction() bankTransaction {
	return bankTransaction{
		AccountID:            "acc-1",
		TransactionID:        "tx-1",
		CreditDebitIndicator: "Debit",
		Status:               "Booked",
		BookingDateTime:      "2026-11-07T09:15:00+03:00",
		Amount:               bankAmount{Amount: "350.00", Currency: "RUB"},
	}
}

func TestNormalizeTransactionErrors(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*bankTransaction)
	}{
		{"empty id", func(tx *bankTransaction) { tx.TransactionID = "" }},
		{"other account", func(tx *bankTransaction) { tx.AccountID = "acc-2" }},
		{"bad amount", func(tx *bankTransaction) { tx.Amount.Amount = "350,00" }},
		{"unknown status", func(tx *bankTransaction) { tx.Status = "Cancelled" }},
		{"booked without date", func(tx *bankTransaction) { tx.BookingDateTime = "" }},
		{"date without zone", func(tx *bankTransaction) { tx.BookingDateTime = "2026-11-07T09:15:00" }},
		{"pending without any date", func(tx *bankTransaction) {
			tx.Status = "Pending"
			tx.BookingDateTime = ""
			tx.ValueDateTime = ""
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tx := validBankTransaction()
			tt.mutate(&tx)
			_, err := normalizeTransaction(tx, "acc-1")
			assertInvalid(t, err)
		})
	}
}

// Pending может прийти без даты проведения.
func TestPendingWithoutBookingDate(t *testing.T) {
	tx := validBankTransaction()
	tx.Status = "Pending"
	tx.ValueDateTime = "2026-11-07T09:15:00+03:00"
	tx.BookingDateTime = ""

	got, err := normalizeTransaction(tx, "acc-1")
	if err != nil {
		t.Fatal(err)
	}
	if got.BookedAt != nil {
		t.Errorf("BookedAt = %v, want nil", got.BookedAt)
	}
}

// ---------- Сломанный ответ ----------

func TestParseBrokenJSON(t *testing.T) {
	broken := []byte("<html>502 Bad Gateway</html>")

	_, err := ParseAccounts(broken)
	assertInvalid(t, err)
	_, err = ParseBalance(broken, "acc-1")
	assertInvalid(t, err)
	_, err = ParseTransactions(broken, "acc-1")
	assertInvalid(t, err)
}
