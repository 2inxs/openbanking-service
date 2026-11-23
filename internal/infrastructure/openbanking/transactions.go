package openbanking

import (
	"encoding/json"
	"fmt"
	"time"

	"github.com/2inxs/openbanking-service/internal/domain"
	"github.com/2inxs/openbanking-service/internal/usecase/banksync"
)

func normalizeTransaction(tx bankTransaction, accountID string) (banksync.BankTransaction, error) {
	if tx.TransactionID == "" {
		return banksync.BankTransaction{}, fmt.Errorf("%w: transaction of account %s: empty transactionId", ErrInvalidBankData, accountID)
	}
	if tx.AccountID != accountID {
		return banksync.BankTransaction{}, fmt.Errorf("%w: transaction %s: accountId %q, requested %q", ErrInvalidBankData, tx.TransactionID, tx.AccountID, accountID)
	}
	value, err := signedMoney(tx.Amount, tx.CreditDebitIndicator)
	if err != nil {
		return banksync.BankTransaction{}, fmt.Errorf("%w: transaction %s: %v", ErrInvalidBankData, tx.TransactionID, err)
	}
	status, err := transactionStatus(tx.Status)
	if err != nil {
		return banksync.BankTransaction{}, fmt.Errorf("%w: transaction %s: %v", ErrInvalidBankData, tx.TransactionID, err)
	}
	bookedAt, err := parseOptionalTime(tx.BookingDateTime)
	if err != nil {
		return banksync.BankTransaction{}, fmt.Errorf("%w: transaction %s: bookingDateTime: %v", ErrInvalidBankData, tx.TransactionID, err)
	}
	valueAt, err := parseOptionalTime(tx.ValueDateTime)
	if err != nil {
		return banksync.BankTransaction{}, fmt.Errorf("%w: transaction %s: valueDateTime: %v", ErrInvalidBankData, tx.TransactionID, err)
	}
	if bookedAt == nil && valueAt == nil {
		return banksync.BankTransaction{}, fmt.Errorf("%w: transaction %s: neither bookingDateTime nor valueDateTime", ErrInvalidBankData, tx.TransactionID)
	}
	if status == domain.TransactionBooked && bookedAt == nil {
		return banksync.BankTransaction{}, fmt.Errorf("%w: transaction %s: booked transaction without bookingDateTime", ErrInvalidBankData, tx.TransactionID)
	}
	var merchantName string
	var mcc string
	if tx.MerchantDetails != nil {
		merchantName = tx.MerchantDetails.MerchantName
		mcc = tx.MerchantDetails.MerchantCategoryCode
	}
	return banksync.BankTransaction{
		ExternalID:   tx.TransactionID,
		Status:       status,
		RawStatus:    tx.Status,
		Value:        value,
		BookedAt:     bookedAt,
		ValueAt:      valueAt,
		Description:  tx.TransactionInformation,
		MerchantName: merchantName,
		MCC:          mcc,
	}, nil
}

func transactionStatus(status string) (domain.TransactionStatusID, error) {
	switch status {
	case "Booked":
		return domain.TransactionBooked, nil
	case "Pending":
		return domain.TransactionPending, nil
	case "Rejected":
		return domain.TransactionRejected, nil
	default:
		return 0, fmt.Errorf("unknown status %q", status)
	}
}

func parseOptionalTime(date string) (*time.Time, error) {
	if date == "" {
		return nil, nil
	}
	t, err := time.Parse(time.RFC3339, date)
	if err != nil {
		return nil, fmt.Errorf("%q is not RFC 3339 date", date)
	}
	return &t, nil
}

func ParseTransactions(data []byte, accountID string) ([]banksync.BankTransaction, error) {
	if accountID == "" {
		return nil, fmt.Errorf("%w: empty requested accountId", ErrInvalidBankData)
	}
	var resp transactionsResponse
	if err := json.Unmarshal(data, &resp); err != nil {
		return nil, fmt.Errorf("%w: transaction response: %v", ErrInvalidBankData, err)
	}
	if resp.Data.Transaction == nil {
		return nil, fmt.Errorf("%w: transactions response: missing Data.Account", ErrInvalidBankData)
	}
	transactions := make([]banksync.BankTransaction, 0, len(resp.Data.Transaction))
	for _, tx := range resp.Data.Transaction {
		normalized, err := normalizeTransaction(tx, accountID)
		if err != nil {
			return nil, err
		}
		transactions = append(transactions, normalized)
	}
	return transactions, nil
}
