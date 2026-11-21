package cbr

import (
	"context"
	"time"

	"github.com/2inxs/openbanking-service/internal/domain/money"
	"github.com/2inxs/openbanking-service/internal/usecase/currency"
)

type RateProviderAdapter struct {
	client *Client
}

func NewRateProviderAdapter(client *Client) *RateProviderAdapter {
	return &RateProviderAdapter{client: client}
}

func (a *RateProviderAdapter) FetchLatest(ctx context.Context) (currency.DailyRates, error) {
	daily, err := a.client.FetchLatest(ctx)
	if err != nil {
		return currency.DailyRates{}, err
	}
	return toUsecaseRates(daily), nil
}

func (a *RateProviderAdapter) FetchByDate(ctx context.Context, date time.Time) (currency.DailyRates, error) {
	daily, err := a.client.FetchByDate(ctx, date)
	if err != nil {
		return currency.DailyRates{}, err
	}
	return toUsecaseRates(daily), nil
}

func toUsecaseRates(daily DailyRates) currency.DailyRates {
	out := make(map[money.Currency]currency.Rate, len(daily.Rates))
	for code, r := range daily.Rates {
		out[code] = currency.Rate{
			Currency:   r.Currency,
			RateDate:   r.RateDate,
			RUBPerUnit: r.RUBPerUnit,
		}
	}
	return currency.DailyRates{
		Date:  daily.Date,
		Rates: out,
	}
}
