package account

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/2inxs/openbanking-service/internal/domain"
	"github.com/2inxs/openbanking-service/internal/domain/money"
	"github.com/2inxs/openbanking-service/pkg/logger"
)

var (
	ErrAccountNotFound = domain.ErrAccountNotFound
	ErrInvalidInput    = domain.ErrInvalidModel
)

const maxStrictIDLen = 128

// AccountBalance представляет последний известный остаток по счёту и момент его актуальности.
type AccountBalance struct {
	Amount   money.Amount   `json:"amount"`
	Currency money.Currency `json:"currency"`
	AsOf     time.Time      `json:"as_of"`
}

// AccountSummary представляет безопасную для отображения модель счёта пользователя.
type AccountSummary struct {
	ID                domain.AccountID        `json:"id"`
	UserID            domain.UserID           `json:"user_id"`
	BankID            domain.BankID           `json:"bank_id"`
	ConnectionID      domain.BankConnectionID `json:"connection_id,omitempty"`
	ExternalAccountID string                  `json:"external_account_id,omitempty"`
	Number            string                  `json:"account_number,omitempty"`
	Name              string                  `json:"name"`
	Type              domain.AccountTypeID    `json:"type"`
	Status            domain.AccountStatusID  `json:"status"`
	Currency          money.Currency          `json:"currency"`
	Balance           *AccountBalance         `json:"balance"` // nil — остаток неизвестен
}

type Service struct {
	repo Repository
}

func NewService(repo Repository) *Service {
	return &Service{repo: repo}
}

func validateStrictID(raw string) error {
	if raw == "" || len(raw) > maxStrictIDLen || raw != strings.TrimSpace(raw) {
		return ErrInvalidInput
	}
	for i := 0; i < len(raw); i++ {
		if raw[i] <= 0x20 || raw[i] == 0x7f {
			return ErrInvalidInput
		}
	}
	return nil
}

func checkContextUserMatch(ctx context.Context, userID domain.UserID) error {
	if ctxUser := strings.TrimSpace(logger.FieldsFromContext(ctx).UserID); ctxUser != "" && ctxUser != string(userID) {
		return ErrAccountNotFound
	}
	return nil
}

func validateAndMapAccount(a domain.Account) (AccountSummary, error) {
	if err := a.Validate(); err != nil {
		return AccountSummary{}, fmt.Errorf("%w: %v", domain.ErrInvalidModel, err)
	}
	summary := AccountSummary{
		ID:                a.ID,
		UserID:            a.UserID,
		BankID:            a.BankID,
		ConnectionID:      a.ConnectionID,
		ExternalAccountID: a.ExternalAccountID,
		Number:            a.Number,
		Name:              a.Name,
		Type:              a.Type,
		Status:            a.Status,
		Currency:          a.Currency,
	}
	if a.Balance != nil {
		if err := a.Balance.Value.Validate(); err != nil {
			return AccountSummary{}, fmt.Errorf("%w: %v", domain.ErrInvalidModel, err)
		}
		if a.Balance.Value.Currency != a.Currency {
			return AccountSummary{}, fmt.Errorf("%w: balance currency %s does not match account currency %s",
				money.ErrCurrencyMismatch, a.Balance.Value.Currency, a.Currency)
		}
		summary.Balance = &AccountBalance{
			Amount:   a.Balance.Value.Amount,
			Currency: a.Balance.Value.Currency,
			AsOf:     a.Balance.AsOf.UTC(),
		}
	}
	return summary, nil
}

// ListAccounts возвращает все счета указанного пользователя с валютой и последним известным остатком.
func (s *Service) ListAccounts(ctx context.Context, userID domain.UserID) ([]AccountSummary, error) {
	if s == nil || s.repo == nil {
		return nil, ErrInvalidInput
	}
	if err := validateStrictID(string(userID)); err != nil {
		return nil, err
	}
	if err := checkContextUserMatch(ctx, userID); err != nil {
		return nil, err
	}

	accounts, err := s.repo.ListByUser(ctx, userID)
	if err != nil {
		return nil, err
	}

	result := make([]AccountSummary, 0, len(accounts))
	for _, a := range accounts {
		if a.UserID != userID {
			continue
		}
		mapped, err := validateAndMapAccount(a)
		if err != nil {
			return nil, err
		}
		result = append(result, mapped)
	}

	sort.Slice(result, func(i, j int) bool {
		if result[i].Name != result[j].Name {
			return result[i].Name < result[j].Name
		}
		return result[i].ID < result[j].ID
	})
	return result, nil
}

// GetAccount возвращает конкретный счёт пользователя или ErrAccountNotFound,
// если счёт не существует или принадлежит другому пользователю.
func (s *Service) GetAccount(ctx context.Context, userID domain.UserID, accountID domain.AccountID) (AccountSummary, error) {
	if s == nil || s.repo == nil {
		return AccountSummary{}, ErrInvalidInput
	}
	if err := validateStrictID(string(userID)); err != nil {
		return AccountSummary{}, err
	}
	if err := validateStrictID(string(accountID)); err != nil {
		return AccountSummary{}, err
	}
	if err := checkContextUserMatch(ctx, userID); err != nil {
		return AccountSummary{}, err
	}

	a, err := s.repo.Get(ctx, userID, accountID)
	if err != nil {
		if errors.Is(err, domain.ErrAccountNotFound) {
			return AccountSummary{}, ErrAccountNotFound
		}
		return AccountSummary{}, err
	}
	if a.UserID != userID || a.ID != accountID {
		return AccountSummary{}, ErrAccountNotFound
	}

	return validateAndMapAccount(a)
}
