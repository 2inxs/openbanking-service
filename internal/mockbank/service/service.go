package service

import (
	"errors"
	"fmt"

	"github.com/2inxs/openbanking-service/internal/mockbank/model"
	"github.com/2inxs/openbanking-service/internal/mockbank/store"
)

var ErrNotFound = errors.New("not found")

type Store interface {
	GetAccount(id string) (model.Account, error)
	GetAccountsByClient(clientID string) []model.Account
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
