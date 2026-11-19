package domain

// Это внутренние ID приложения, не коды банка. Будущие справочники БД должны использовать те же значения.
type ConnectionStatusID int16

const (
	ConnectionActive       ConnectionStatusID = 1
	ConnectionError        ConnectionStatusID = 2
	ConnectionDisconnected ConnectionStatusID = 3
	ConnectionPending      ConnectionStatusID = 4
)

func (s ConnectionStatusID) Valid() bool {
	switch s {
	case ConnectionActive, ConnectionError, ConnectionDisconnected, ConnectionPending:
		return true
	default:
		return false
	}
}

type TransactionStatusID int16

const (
	TransactionPending  TransactionStatusID = 1
	TransactionBooked   TransactionStatusID = 2
	TransactionRejected TransactionStatusID = 3
)

func (s TransactionStatusID) Valid() bool {
	switch s {
	case TransactionPending, TransactionBooked, TransactionRejected:
		return true
	default:
		return false
	}
}

type TransactionSourceID int16

const (
	SourceOpenBanking TransactionSourceID = 1
	SourceStatement   TransactionSourceID = 2
)

func (s TransactionSourceID) Valid() bool {
	return s == SourceOpenBanking || s == SourceStatement
}
