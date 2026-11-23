package cache

import (
	"math/big"
	"sync/atomic"
	"time"

	"github.com/2inxs/openbanking-service/internal/domain"
	"github.com/2inxs/openbanking-service/internal/domain/money"
)

type rateSnapshot struct {
	rateDate  time.Time
	fetchedAt time.Time
	rates     map[money.Currency]*big.Rat
}

// RateCache хранит неизменяемый снимок актуальных курсов в памяти.
type RateCache struct {
	snap atomic.Pointer[rateSnapshot]
}

func NewRateCache() *RateCache {
	return &RateCache{}
}

// Replace атомарно заменяет снимок курсов новым набором.
func (c *RateCache) Replace(daily domain.DailyRates, fetchedAt time.Time) {
	cloned := make(map[money.Currency]*big.Rat, len(daily.Rates))
	for curr, rate := range daily.Rates {
		if rate.RUBPerUnit != nil && rate.RUBPerUnit.Sign() > 0 {
			cloned[curr] = new(big.Rat).Set(rate.RUBPerUnit)
		}
	}
	c.snap.Store(&rateSnapshot{
		rateDate:  daily.Date,
		fetchedAt: fetchedAt,
		rates:     cloned,
	})
}

// Get возвращает копию курса из снимка. Второй аргумент false, если снимок пуст или валюты нет.
func (c *RateCache) Get(curr money.Currency) (domain.ExchangeRate, bool) {
	s := c.snap.Load()
	if s == nil {
		return domain.ExchangeRate{}, false
	}
	if curr == money.RUB {
		return domain.ExchangeRate{
			Currency:   money.RUB,
			RateDate:   s.rateDate,
			RUBPerUnit: big.NewRat(1, 1),
		}, true
	}
	rat, ok := s.rates[curr]
	if !ok {
		return domain.ExchangeRate{}, false
	}
	return domain.ExchangeRate{
		Currency:   curr,
		RateDate:   s.rateDate,
		RUBPerUnit: new(big.Rat).Set(rat),
	}, true
}

// Meta возвращает дату действия текущего снимка и время его загрузки.
func (c *RateCache) Meta() (rateDate time.Time, fetchedAt time.Time, ok bool) {
	s := c.snap.Load()
	if s == nil {
		return time.Time{}, time.Time{}, false
	}
	return s.rateDate, s.fetchedAt, true
}
