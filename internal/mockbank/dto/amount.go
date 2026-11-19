package dto

// Значения creditDebitIndicator по стандарту.
const (
	IndicatorCredit = "Credit"
	IndicatorDebit  = "Debit"
)

// AmountDTO — сумма в формате стандарта: строка без знака и код валюты.
type AmountDTO struct {
	Amount   string `json:"amount"`
	Currency string `json:"currency"`
}

// ToSignedAmount переводит сумму со знаком (в минимальных единицах) в формат стандарта.
// Стандарт передаёт сумму без знака, а направление — отдельно, через creditDebitIndicator:
// неотрицательная сумма → Credit, отрицательная → Debit с модулем суммы.
func ToSignedAmount(minor int64, currency string) (AmountDTO, string) {
	indicator := IndicatorCredit
	if minor < 0 {
		indicator = IndicatorDebit
		minor = -minor
	}
	return AmountDTO{Amount: FormatAmount(minor), Currency: currency}, indicator
}
