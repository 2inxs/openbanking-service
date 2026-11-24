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

type supportedCurrencyLister interface {
	SupportedCurrencies(ctx context.Context) (map[money.Currency]struct{}, error)
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

func (s *Service) filterSupported(ctx context.Context, daily domain.DailyRates) (domain.DailyRates, error) {
	lister, ok := s.repo.(supportedCurrencyLister)
	if !ok {
		return daily, nil
	}
	supported, err := lister.SupportedCurrencies(ctx)
	if err != nil {
		return domain.DailyRates{}, err
	}
	filtered := make(map[money.Currency]domain.ExchangeRate, len(daily.Rates))
	for code, rate := range daily.Rates {
		if _, exists := supported[code]; exists {
			filtered[code] = rate
		}
	}
	return domain.DailyRates{
		Date:  daily.Date,
		Rates: filtered,
	}, nil
}

// Warmup загружает последние сохранённые курсы из БД при старте
// и возвращает true, если курсы отсутствуют или устарели (нужно обновление).
func (s *Service) Warmup(ctx context.Context) (bool, error) {
	now := s.nowFn()

	candidates := []money.Currency{money.Currency("USD"), money.Currency("EUR"), money.Currency("JPY")}
	if lister, ok := s.repo.(supportedCurrencyLister); ok {
		supported, err := lister.SupportedCurrencies(ctx)
		if err != nil {
			return true, fmt.Errorf("currency: warmup load supported currencies: %w", err)
		}
		candidates = candidates[:0]
		for code := range supported {
			if code != money.RUB {
				candidates = append(candidates, code)
			}
		}
	}

	loaded := make(map[money.Currency]domain.ExchangeRate, len(candidates))
	var latestDate time.Time

	for _, code := range candidates {
		rate, err := s.repo.GetRate(ctx, code, nil)
		if err != nil {
			if errors.Is(err, ErrRateNotFound) {
				continue
			}
			return true, fmt.Errorf("currency: warmup read latest rate for %s: %w", code, err)
		}
		loaded[code] = rate
		if rate.RateDate.After(latestDate) {
			latestDate = rate.RateDate
		}
	}

	if len(loaded) == 0 {
		return true, nil
	}

	if s.cache != nil {
		s.cache.Replace(domain.DailyRates{
			Date:  latestDate,
			Rates: loaded,
		}, now.UTC())
	}
	return isStale(latestDate, now), nil
}

func (s *Service) Refresh(ctx context.Context) error {
	daily, err := s.provider.FetchLatest(ctx)
	if err != nil {
		return fmt.Errorf("currency: fetch latest rates: %w", err)
	}

	s.syncMu.Lock()
	defer s.syncMu.Unlock()

	filtered, err := s.filterSupported(ctx, daily)
	if err != nil {
		return fmt.Errorf("currency: filter supported rates: %w", err)
	}
	if err := s.repo.SaveBatch(ctx, filtered); err != nil {
		return fmt.Errorf("currency: save rates batch: %w", err)
	}
	if s.cache != nil {
		currentDate, _, ok := s.cache.Meta()
		if !ok || !filtered.Date.Before(currentDate) {
			s.cache.Replace(filtered, s.nowFn().UTC())
		}
	}
	return nil
}
func (s *Service) SyncByDate(ctx context.Context, date time.Time) error {
	daily, err := s.provider.FetchByDate(ctx, date)
	if err != nil {
		return fmt.Errorf("currency: fetch rates by date: %w", err)
	}
	s.syncMu.Lock()
	defer s.syncMu.Unlock()
	filtered, err := s.filterSupported(ctx, daily)
	if err != nil {
		return fmt.Errorf("currency: filter supported rates: %w", err)
	}
	if err := s.repo.SaveBatch(ctx, filtered); err != nil {
		return fmt.Errorf("currency: save rates batch: %w", err)
	}
	if s.cache != nil {
		currentDate, _, ok := s.cache.Meta()
		if !ok || !filtered.Date.Before(currentDate) {
			s.cache.Replace(filtered, s.nowFn().UTC())
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
