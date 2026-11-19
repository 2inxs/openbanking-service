package model

import "time"

type CreditDebit string

const (
	Credit CreditDebit = "Credit"
	Debit  CreditDebit = "Debit"
)

type TransactionStatus string

const (
	TransactionBooked  TransactionStatus = "Booked"
	TransactionPending TransactionStatus = "Pending"
)

type AccountStatus string

const (
	Enabled AccountStatus = "Enabled"
	Deleted AccountStatus = "Deleted"
)

type AccountSubType string

const (
	CurrentAccount AccountSubType = "CurrentAccount"
	CreditCard     AccountSubType = "CreditCard"
	Savings        AccountSubType = "Saving"
)

type Client struct {
	ID       string
	FullName string
}

type Account struct {
	ID          string
	ClientID    string
	Number      string
	Currency    string
	SubType     AccountSubType
	Status      AccountStatus
	Description string
	BalanceKop  int64
	Card        *Card
	OpenedAt    time.Time
}

type Card struct {
	Scheme       string
	MaskedNumber string
	HolderName   string
}

type Transaction struct {
	ID           string
	AccountID    string
	Amount       int64
	Direction    CreditDebit
	Status       TransactionStatus
	BookedAt     time.Time
	ValueAt      time.Time
	Description  string
	MerchantName string
	MCC          string
	ByCard       bool
}
