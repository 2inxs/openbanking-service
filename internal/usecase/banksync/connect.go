package banksync

import (
	"context"
	"errors"

	"github.com/2inxs/openbanking-service/internal/domain"
)

type Service struct {
	banks       BankRepository
	connections ConnectionRepository
	clients     map[domain.BankCode]BankClient
	ids         IDGenerator
}

func NewService(banks BankRepository, connections ConnectionRepository, clients map[domain.BankCode]BankClient, ids IDGenerator) *Service {
	return &Service{banks: banks, connections: connections, clients: clients, ids: ids}
}

// ConnectBank подключает банк пользователю или возвращает существующее подключение
// created = true, если подключение создано этим вызовом
func (s *Service) ConnectBank(ctx context.Context, user domain.UserID, code domain.BankCode) (domain.BankConnection, bool, error) {
	bankID, err := s.banks.GetByCode(ctx, code)
	if err != nil {
		return domain.BankConnection{}, false, err
	}
	client, ok := s.clients[code]
	if !ok {
		return domain.BankConnection{}, false, ErrBankNotFound
	}
	existing, err := s.connections.GetByBank(ctx, user, bankID)
	if err == nil {
		return existing, false, nil
	}
	if !errors.Is(err, ErrConnectionNotFound) {
		return domain.BankConnection{}, false, err
	}
	if _, err := client.Accounts(ctx); err != nil {
		return domain.BankConnection{}, false, err
	}
	id, err := s.ids.NewID()
	if err != nil {
		return domain.BankConnection{}, false, err
	}
	created, err := s.connections.Create(ctx, domain.BankConnection{
		ID: domain.BankConnectionID(id), UserID: user, BankID: bankID, Status: domain.ConnectionActive,
	})
	if errors.Is(err, ErrConnectionConflict) {
		// Параллельный запрос успел создать подключение раньше — возвращаем его.
		winner, err := s.connections.GetByBank(ctx, user, bankID)
		return winner, false, err
	}
	if err != nil {
		return domain.BankConnection{}, false, err
	}
	return created, true, nil
}

// GetUserConnection возвращает подключение пользователя из БД
func (s *Service) GetUserConnection(ctx context.Context, user domain.UserID, id domain.BankConnectionID) (domain.BankConnection, error) {
	conn, err := s.connections.Get(ctx, user, id)
	if errors.Is(err, domain.ErrInvalidModel) {
		return domain.BankConnection{}, ErrConnectionNotFound
	}
	return conn, err
}
