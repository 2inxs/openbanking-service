package currency

import (
	"context"
	"errors"
	"fmt"
	"math/big"
	"sync"
	"time"

	"github.com/2inxs/openbanking-service/internal/domain/money"
)

const RateScale = 10

var (
	ErrRateNotFound    = errors.New("currency: rate not found")
	ErrInvalidCurrency = errors.New("currency: invalid currency code")
)

// Rate — доменная модель курса 1 единицы валюты к RUB на фактическую дату действия.
type Rate struct {
	Currency   money.Currency
	RateDate   time.Time
	RUBPerUnit *big.Rat
}

// RUBPerUnitString возвращает курс десятичной строкой с 10 знаками после запятой (для JSON и numeric(24,10)).
func (r Rate) RUBPerUnitString() string {
	if r.RUBPerUnit == nil {
		return "0.0000000000"
	}
	return r.RUBPerUnit.FloatString(RateScale)
}

// DailyRates — набор курсов на конкретную дату действия ЦБ РФ.
type DailyRates struct {
	Date  time.Time
	Rates map[money.Currency]Rate
}

// RateProvider описывает источник свежих курсов (адаптер над cbr.Client).
type RateProvider interface {
	FetchLatest(ctx context.Context) (DailyRates, error)
	FetchByDate(ctx context.Context, date time.Time) (DailyRates, error)
}

// RateRepository описывает атомарное и идемпотентное хранилище курсов (memory / postgres).
type RateRepository interface {
	SaveBatch(ctx context.Context, rates DailyRates) error
	GetRate(ctx context.Context, curr money.Currency, date *time.Time) (Rate, error)
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

// Refresh запрашивает актуальные курсы у провайдера вне транзакции и атомарно сохраняет их.
// Имя метода совпадает с контрактом фонового воркера из раздела 8 архитектуры.
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

// SyncByDate запрашивает курсы на указанную дату вне транзакции и сохраняет под фактической датой действия.
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

// GetRate возвращает курс валюты на указанную дату (или последний доступный, если date == nil).
func (s *Service) GetRate(ctx context.Context, curr money.Currency, date *time.Time) (Rate, error) {
	if err := curr.Validate(); err != nil {
		return Rate{}, fmt.Errorf("%w: %v", ErrInvalidCurrency, err)
	}
	if curr == money.RUB {
		d := time.Now().UTC().Truncate(24 * time.Hour)
		if date != nil {
			d = time.Date(date.Year(), date.Month(), date.Day(), 0, 0, 0, 0, time.UTC)
		}
		return Rate{
			Currency:   money.RUB,
			RateDate:   d,
			RUBPerUnit: big.NewRat(1, 1),
		}, nil
	}
	return s.repo.GetRate(ctx, curr, date)
}
