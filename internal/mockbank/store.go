package mockbank

import (
	"errors"
	"fmt"
	"slices"
	"sync"
)

var ErrNotFound = errors.New("не найден")
var ErrAlreadyExists = errors.New("уже существует")

type Store struct {
	mu                    sync.RWMutex
	clients               map[string]Client
	accounts              map[string]Account
	accountIDsByClient    map[string][]string
	transactionsByAccount map[string][]Transaction
}

func NewStore() *Store {
	return &Store{
		clients:               make(map[string]Client),
		accounts:              make(map[string]Account),
		accountIDsByClient:    make(map[string][]string),
		transactionsByAccount: make(map[string][]Transaction),
	}
}

func (s *Store) GetClient(id string) (Client, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	client, ok := s.clients[id]
	if !ok {
		return Client{}, ErrNotFound
	}
	return client, nil
}

func (s *Store) GetAccount(id string) (Account, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	account, ok := s.accounts[id]
	if !ok {
		return Account{}, ErrNotFound
	}
	return account, nil
}

func (s *Store) GetAccountsByClient(clientID string) []Account {
	s.mu.RLock()
	defer s.mu.RUnlock()
	ids := s.accountIDsByClient[clientID]
	accounts := make([]Account, 0, len(ids))
	for _, id := range ids {
		account, ok := s.accounts[id]
		if !ok {
			continue
		}
		accounts = append(accounts, account)
	}
	return accounts
}

func (s *Store) GetTransactionsByAccount(accountID string) ([]Transaction, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if _, ok := s.accounts[accountID]; !ok {
		return nil, ErrNotFound
	}
	txs := slices.Clone(s.transactionsByAccount[accountID])
	slices.SortFunc(txs, func(a, b Transaction) int {
		return b.BookedAt.Compare(a.BookedAt)
	})
	return txs, nil
}

func (s *Store) AddClient(c Client) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, exists := s.clients[c.ID]; exists {
		return fmt.Errorf("клиент %q: %w", c.ID, ErrAlreadyExists)
	}
	s.clients[c.ID] = c
	return nil
}

func (s *Store) AddAccount(acc Account) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.clients[acc.ClientID]; !ok {
		return fmt.Errorf("клиент %q: %w", acc.ClientID, ErrNotFound)
	}
	if _, exists := s.accounts[acc.ID]; exists {
		return fmt.Errorf("счет %q: %w", acc.ID, ErrAlreadyExists)
	}
	s.accounts[acc.ID] = acc
	s.accountIDsByClient[acc.ClientID] = append(s.accountIDsByClient[acc.ClientID], acc.ID)
	return nil
}

func (s *Store) AddTransaction(trx Transaction) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	acc, ok := s.accounts[trx.AccountID]
	if !ok {
		return fmt.Errorf("счет %q: %w", trx.AccountID, ErrNotFound)
	}
	if trx.AmountKop <= 0 {
		return fmt.Errorf("неверная сумма %d", trx.AmountKop)
	}
	switch trx.Direction {
	case Credit:
		acc.BalanceKop += trx.AmountKop
	case Debit:
		acc.BalanceKop -= trx.AmountKop
	default:
		return fmt.Errorf("неизвестный счет %q", trx.Direction)
	}
	s.accounts[trx.AccountID] = acc
	s.transactionsByAccount[acc.ID] = append(s.transactionsByAccount[acc.ID], trx)
	return nil
}
