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

type RateCache interface {
	Replace(daily domain.DailyRates, fetchedAt time.Time)
	Get(curr money.Currency) (domain.ExchangeRate, bool)
	Meta() (rateDate time.Time, fetchedAt time.Time, ok bool)
}

type Service struct {
	provider RateProvider
	repo     RateRepository
	cache    RateCache
	nowFn    func() time.Time
	syncMu   sync.Mutex
}

func NewService(provider RateProvider, repo RateRepository) *Service {
	return &Service{
		provider: provider,
		repo:     repo,
		nowFn:    time.Now,
	}
}

func (s *Service) WithCache(c RateCache) *Service {
	s.cache = c
	return s
}

// Warmup загружает последний сохранённый курс из БД при старте
// и возвращает true, если курсы отсутствуют или устарели (нужно обновление).
func (s *Service) Warmup(ctx context.Context) (bool, error) {
	now := s.nowFn()
	rate, err := s.repo.GetRate(ctx, money.Currency("USD"), nil)
	if err != nil {
		if errors.Is(err, ErrRateNotFound) {
			return true, nil
		}
		return true, fmt.Errorf("currency: warmup read latest rate: %w", err)
	}
	if s.cache != nil {
		s.cache.Replace(domain.DailyRates{
			Date:  rate.RateDate,
			Rates: map[money.Currency]domain.ExchangeRate{rate.Currency: rate},
		}, now.UTC())
	}
	return isStale(rate.RateDate, now), nil
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
	// Кеш обновляем строго после успешного сохранения в БД.
	if s.cache != nil {
		s.cache.Replace(daily, s.nowFn().UTC())
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
	if s.cache != nil {
		currentDate, _, ok := s.cache.Meta()
		if !ok || !daily.Date.Before(currentDate) {
			s.cache.Replace(daily, s.nowFn().UTC())
		}
	}
	return nil
}

func (s *Service) GetRate(ctx context.Context, curr money.Currency, date *time.Time) (domain.ExchangeRate, error) {
	if err := curr.Validate(); err != nil {
		return domain.ExchangeRate{}, fmt.Errorf("%w: %v", ErrInvalidCurrency, err)
	}
	if curr == money.RUB {
		d := s.nowFn().UTC().Truncate(24 * time.Hour)
		if date != nil {
			d = time.Date(date.Year(), date.Month(), date.Day(), 0, 0, 0, 0, time.UTC)
		} else if s.cache != nil {
			if rateDate, _, ok := s.cache.Meta(); ok && !rateDate.IsZero() {
				d = rateDate
			}
		}
		return domain.ExchangeRate{
			Currency:   money.RUB,
			RateDate:   d,
			RUBPerUnit: big.NewRat(1, 1),
		}, nil
	}
	if date == nil && s.cache != nil {
		if cached, ok := s.cache.Get(curr); ok {
			return cached, nil
		}
	}
	return s.repo.GetRate(ctx, curr, date)
}
