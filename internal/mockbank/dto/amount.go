package dto

const (
	IndicatorCredit = "Credit"
	IndicatorDebit  = "Debit"
)

type AmountDTO struct {
	Amount   string `json:"amount"`
	Currency string `json:"currency"`
}

func ToSignedAmount(minor int64, currency string) (AmountDTO, string) {
	indicator := IndicatorCredit
	if minor < 0 {
		indicator = IndicatorDebit
		minor = -minor
	}
	return AmountDTO{Amount: FormatAmount(minor), Currency: currency}, indicator
}
