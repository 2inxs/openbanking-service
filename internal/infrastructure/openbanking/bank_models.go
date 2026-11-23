package openbanking

type accountsResponse struct {
	Data accountsData `json:"Data"`
}

type accountsData struct {
	Account []bankAccount `json:"Account"`
}

type bankAccount struct {
	AccountID          string               `json:"accountId"`
	Status             string               `json:"status"`
	Currency           string               `json:"currency"`
	AccountType        string               `json:"accountType"`
	AccountSubType     string               `json:"accountSubType"`
	AccountDescription string               `json:"accountDescription"`
	AccountDetails     []bankAccountDetails `json:"AccountDetails"`
}

type bankAccountDetails struct {
	SchemeName     string `json:"schemeName"`
	Identification string `json:"identification"`
	Name           string `json:"name"`
}

type balancesResponse struct {
	Data balancesData `json:"Data"`
}

type balancesData struct {
	Balance []bankBalance `json:"Balance"`
}

type bankBalance struct {
	AccountID            string     `json:"accountId"`
	CreditDebitIndicator string     `json:"creditDebitIndicator"`
	Type                 string     `json:"type"`
	DateTime             string     `json:"dateTime"`
	Amount               bankAmount `json:"Amount"`
}

type transactionsResponse struct {
	Data transactionsData `json:"Data"`
}

type transactionsData struct {
	Transaction []bankTransaction `json:"Transaction"`
}

type bankTransaction struct {
	AccountID              string               `json:"accountId"`
	TransactionID          string               `json:"transactionId"`
	CreditDebitIndicator   string               `json:"creditDebitIndicator"`
	Status                 string               `json:"status"`
	BookingDateTime        string               `json:"bookingDateTime"`
	ValueDateTime          string               `json:"valueDateTime"`
	TransactionInformation string               `json:"transactionInformation"`
	Amount                 bankAmount           `json:"Amount"`
	MerchantDetails        *bankMerchantDetails `json:"MerchantDetails"`
}

type bankMerchantDetails struct {
	MerchantName         string `json:"merchantName"`
	MerchantCategoryCode string `json:"merchantCategoryCode"`
}

type bankAmount struct {
	Amount   string `json:"amount"`
	Currency string `json:"currency"`
}
