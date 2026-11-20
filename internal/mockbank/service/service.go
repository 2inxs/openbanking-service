package service

import (
	"errors"
	"fmt"
	"time"

	"github.com/2inxs/openbanking-service/internal/mockbank/model"
	"github.com/2inxs/openbanking-service/internal/mockbank/store"
)

var ErrNotFound = errors.New("not found")
var ErrInvalidPeriod = errors.New("invalid period: from is after to")

type Store interface {
	GetAccount(id string) (model.Account, error)
	GetAccountsByClient(clientID string) []model.Account
	GetTransactionsByAccount(accountID string) ([]model.Transaction, error)
}

type Service struct {
	store Store
}

func New(st Store) *Service {
	return &Service{store: st}
}

// Accounts возвращает все счета клиента, клиент без счетов пустой список, не ошибка
func (s *Service) Accounts(clientID string) []model.Account {
	return s.store.GetAccountsByClient(clientID)
}

// Account возвращает счёт, только если он принадлежит клиенту
func (s *Service) Account(clientID, accountID string) (model.Account, error) {
	acc, err := s.store.GetAccount(accountID)
	if errors.Is(err, store.ErrNotFound) {
		return model.Account{}, ErrNotFound
	}
	if err != nil {
		return model.Account{}, fmt.Errorf("get account %s: %w", accountID, err)
	}
	if acc.ClientID != clientID {
		return model.Account{}, ErrNotFound
	}
	return acc, nil
}

// Transactions возвращает операции по счету, только если счет принадлежит клиенту
func (s *Service) Transactions(clientID, accountID string, from, to time.Time) ([]model.Transaction, error) {
	if !to.IsZero() && !from.IsZero() && from.After(to) {
		return nil, ErrInvalidPeriod
	}
	acc, err := s.Account(clientID, accountID)
	if err != nil {
		return nil, err
	}
	txs, err := s.store.GetTransactionsByAccount(acc.ID)
	if err != nil {
		return nil, fmt.Errorf("get transactions %s: %w", acc.ID, err)
	}
	filtered := make([]model.Transaction, 0, len(txs))
	for _, tx := range txs {
		if !from.IsZero() && tx.BookedAt.Before(from) {
			continue
		}
		if !to.IsZero() && tx.BookedAt.After(to) {
			continue
		}
		filtered = append(filtered, tx)
	}
	return filtered, nil
}
