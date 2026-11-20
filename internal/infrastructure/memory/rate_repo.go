package memory

import (
	"context"
	"sync"
	"time"

	"github.com/2inxs/openbanking-service/internal/domain/money"
	"github.com/2inxs/openbanking-service/internal/infrastructure/cbr"
	"github.com/2inxs/openbanking-service/internal/usecase/currency"
)

type RateRepository struct {
	mu     sync.RWMutex
	byDate map[string]map[money.Currency]cbr.Rate
}

func NewRateRepository() *RateRepository {
	return &RateRepository{
		byDate: make(map[string]map[money.Currency]cbr.Rate),
	}
}

func (r *RateRepository) SaveBatch(_ context.Context, daily cbr.DailyRates) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	dateKey := daily.Date.UTC().Format("2006-01-02")
	bucket := make(map[money.Currency]cbr.Rate, len(daily.Rates))
	for code, rate := range daily.Rates {
		bucket[code] = rate
	}
	r.byDate[dateKey] = bucket
	return nil
}

func (r *RateRepository) GetRate(_ context.Context, curr money.Currency, date *time.Time) (cbr.Rate, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	var bestDate time.Time
	var bestRate cbr.Rate
	found := false

	for _, bucket := range r.byDate {
		rate, ok := bucket[curr]
		if !ok {
			continue
		}
		if date != nil {
			reqDay := time.Date(date.Year(), date.Month(), date.Day(), 0, 0, 0, 0, time.UTC)
			if rate.RateDate.After(reqDay) {
				continue
			}
		}
		if !found || rate.RateDate.After(bestDate) {
			bestDate = rate.RateDate
			bestRate = rate
			found = true
		}
	}

	if !found {
		return cbr.Rate{}, currency.ErrRateNotFound
	}
	return bestRate, nil
}
