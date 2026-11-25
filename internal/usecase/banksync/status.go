package banksync

import (
	"slices"

	"github.com/2inxs/openbanking-service/internal/domain"
)

// allowedTransitions - в какие статусы может перейти подключение
var allowedTransitions = map[domain.ConnectionStatusID][]domain.ConnectionStatusID{
	domain.ConnectionPending:      {domain.ConnectionActive, domain.ConnectionError},
	domain.ConnectionActive:       {domain.ConnectionError, domain.ConnectionDisconnected},
	domain.ConnectionError:        {domain.ConnectionActive, domain.ConnectionDisconnected},
	domain.ConnectionDisconnected: {domain.ConnectionActive}, // только повторным подключением
}

// CanTransition сообщает, разрешён ли переход статуса подключения
func CanTransition(from, to domain.ConnectionStatusID) bool {
	if !from.Valid() || !to.Valid() {
		return false
	}
	if from == to {
		return true
	}
	return slices.Contains(allowedTransitions[from], to)
}
