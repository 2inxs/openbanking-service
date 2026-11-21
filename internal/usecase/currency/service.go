package currency

import (
	"context"
	"errors"
	"fmt"
	"math/big"
	"sync"
	"time"

	"github.com/2inxs/openbanking-service/internal/domain"
	"github.com/2inxs/openbanking-service/internal/domain/money"
)

var (
	ErrRateNotFound    = errors.New("currency: rate not found")
	ErrInvalidCurrency = errors.New("currency: invalid currency code")
)

type RateProvider interface {
	FetchLatest(ctx context.Context) (domain.DailyRates, error)
	FetchByDate(ctx context.Context, date time.Time) (domain.DailyRates, error)
}

type RateRepository interface {
	SaveBatch(ctx context.Context, rates domain.DailyRates) error
	GetRate(ctx context.Context, curr money.Currency, date *time.Time) (domain.ExchangeRate, error)
}

type Service struct {
	provider RateProvider
	repo     RateRepository
	syncMu   sync.Mutex
}

func NewService(provider RateProvider, repo RateRepository) *Service {
	return &Service{
		provider: provider,
		repo:     repo,
	}
}

func (s *Service) Refresh(ctx context.Context) error {
	s.syncMu.Lock()
	defer s.syncMu.Unlock()

	daily, err := s.provider.FetchLatest(ctx)
	if err != nil {
		return fmt.Errorf("currency: fetch latest rates: %w", err)
	}
	if err := s.repo.SaveBatch(ctx, daily); err != nil {
		return fmt.Errorf("currency: save rates batch: %w", err)
	}
	return nil
}

func (s *Service) SyncByDate(ctx context.Context, date time.Time) error {
	s.syncMu.Lock()
	defer s.syncMu.Unlock()

	daily, err := s.provider.FetchByDate(ctx, date)
	if err != nil {
		return fmt.Errorf("currency: fetch rates by date: %w", err)
	}
	if err := s.repo.SaveBatch(ctx, daily); err != nil {
		return fmt.Errorf("currency: save rates batch: %w", err)
	}
	return nil
}

func (s *Service) GetRate(ctx context.Context, curr money.Currency, date *time.Time) (domain.ExchangeRate, error) {
	if err := curr.Validate(); err != nil {
		return domain.ExchangeRate{}, fmt.Errorf("%w: %v", ErrInvalidCurrency, err)
	}
	if curr == money.RUB {
		d := time.Now().UTC().Truncate(24 * time.Hour)
		if date != nil {
			d = time.Date(date.Year(), date.Month(), date.Day(), 0, 0, 0, 0, time.UTC)
		}
		return domain.ExchangeRate{
			Currency:   money.RUB,
			RateDate:   d,
			RUBPerUnit: big.NewRat(1, 1),
		}, nil
	}
	return s.repo.GetRate(ctx, curr, date)
}
