package currency

import (
	"context"
	"errors"
	"fmt"
	"math/big"
	"sync"
	"time"

	"github.com/2inxs/openbanking-service/internal/domain/money"
	"github.com/2inxs/openbanking-service/internal/infrastructure/cbr"
)

var (
	ErrRateNotFound    = errors.New("currency: rate not found")
	ErrInvalidCurrency = errors.New("currency: invalid currency code")
)

type Provider interface {
	FetchLatest(ctx context.Context) (cbr.DailyRates, error)
	FetchByDate(ctx context.Context, date time.Time) (cbr.DailyRates, error)
}

type Repository interface {
	SaveBatch(ctx context.Context, rates cbr.DailyRates) error
	GetRate(ctx context.Context, curr money.Currency, date *time.Time) (cbr.Rate, error)
}

type Service struct {
	provider Provider
	repo     Repository
	syncMu   sync.Mutex
}

func NewService(provider Provider, repo Repository) *Service {
	return &Service{
		provider: provider,
		repo:     repo,
	}
}

func (s *Service) SyncLatest(ctx context.Context) (cbr.DailyRates, error) {
	s.syncMu.Lock()
	defer s.syncMu.Unlock()

	daily, err := s.provider.FetchLatest(ctx)
	if err != nil {
		return cbr.DailyRates{}, fmt.Errorf("fetch latest rates: %w", err)
	}
	if err := s.repo.SaveBatch(ctx, daily); err != nil {
		return cbr.DailyRates{}, fmt.Errorf("save rates batch: %w", err)
	}
	return daily, nil
}

func (s *Service) SyncByDate(ctx context.Context, date time.Time) (cbr.DailyRates, error) {
	s.syncMu.Lock()
	defer s.syncMu.Unlock()

	daily, err := s.provider.FetchByDate(ctx, date)
	if err != nil {
		return cbr.DailyRates{}, fmt.Errorf("fetch rates by date: %w", err)
	}
	if err := s.repo.SaveBatch(ctx, daily); err != nil {
		return cbr.DailyRates{}, fmt.Errorf("save rates batch: %w", err)
	}
	return daily, nil
}

func (s *Service) GetRate(ctx context.Context, curr money.Currency, date *time.Time) (cbr.Rate, error) {
	if err := curr.Validate(); err != nil {
		return cbr.Rate{}, fmt.Errorf("%w: %v", ErrInvalidCurrency, err)
	}
	if curr == money.RUB {
		d := time.Now().UTC().Truncate(24 * time.Hour)
		if date != nil {
			d = time.Date(date.Year(), date.Month(), date.Day(), 0, 0, 0, 0, time.UTC)
		}
		return cbr.Rate{
			Currency:   money.RUB,
			RateDate:   d,
			RUBPerUnit: big.NewRat(1, 1),
		}, nil
	}
	return s.repo.GetRate(ctx, curr, date)
}
