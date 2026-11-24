package postgres

import (
	"context"
	"math/big"
	"sort"
	"strings"
	"time"

	"github.com/2inxs/openbanking-service/internal/domain"
	"github.com/2inxs/openbanking-service/internal/domain/money"
	"github.com/2inxs/openbanking-service/internal/usecase/currency"
)

type RateRepository struct {
	db DBTX
}

var _ currency.RateRepository = (*RateRepository)(nil)

func NewRateRepository(db DBTX) *RateRepository {
	return &RateRepository{db: db}
}

func (r *RateRepository) SaveBatch(ctx context.Context, daily domain.DailyRates) error {
	if daily.Date.IsZero() {
		return domain.ErrInvalidModel
	}
	rateDate := time.Date(daily.Date.Year(), daily.Date.Month(), daily.Date.Day(), 0, 0, 0, 0, time.UTC)

	codes := make([]money.Currency, 0, len(daily.Rates))
	for code := range daily.Rates {
		codes = append(codes, code)
	}
	sort.Slice(codes, func(i, j int) bool { return codes[i] < codes[j] })

	for _, code := range codes {
		rate := daily.Rates[code]
		if err := rate.Validate(); err != nil {
			return err
		}
		if rate.Currency != code {
			return domain.ErrInvalidModel
		}
		valStr := rate.RUBPerUnitString()
		integer := strings.Split(valStr, ".")[0]
		if len(integer) > 14 {
			return domain.ErrInvalidModel
		}

		_, err := r.db.Exec(ctx, `INSERT INTO exchange_rates (currency_code, rate_date, rub_per_unit, fetched_at)
 VALUES ($1, $2::date, $3::numeric, now())
 ON CONFLICT (currency_code, rate_date)
 DO UPDATE SET rub_per_unit = EXCLUDED.rub_per_unit, fetched_at = EXCLUDED.fetched_at`,
			string(rate.Currency), rateDate.Format("2006-01-02"), valStr)
		if err != nil {
			return repositoryError(err, currency.ErrRateNotFound, domain.ErrInvalidModel)
		}
	}
	return nil
}

func (r *RateRepository) GetRate(ctx context.Context, curr money.Currency, date *time.Time) (domain.ExchangeRate, error) {
	if err := curr.Validate(); err != nil {
		return domain.ExchangeRate{}, err
	}

	var (
		codeStr string
		rateDay time.Time
		rateStr string
		err     error
	)

	if date == nil {
		err = r.db.QueryRow(ctx, `SELECT currency_code, rate_date, rub_per_unit::text
 FROM exchange_rates
 WHERE currency_code = $1
 ORDER BY rate_date DESC
 LIMIT 1`, string(curr)).Scan(&codeStr, &rateDay, &rateStr)
	} else {
		reqDay := time.Date(date.Year(), date.Month(), date.Day(), 0, 0, 0, 0, time.UTC)
		err = r.db.QueryRow(ctx, `SELECT currency_code, rate_date, rub_per_unit::text
 FROM exchange_rates
 WHERE currency_code = $1 AND rate_date <= $2::date
 ORDER BY rate_date DESC
 LIMIT 1`, string(curr), reqDay.Format("2006-01-02")).Scan(&codeStr, &rateDay, &rateStr)
	}
	if err != nil {
		return domain.ExchangeRate{}, repositoryError(err, currency.ErrRateNotFound, domain.ErrInvalidModel)
	}

	rat, ok := new(big.Rat).SetString(rateStr)
	if !ok || rat.Sign() <= 0 {
		return domain.ExchangeRate{}, ErrUnavailable
	}

	res := domain.ExchangeRate{
		Currency:   money.Currency(codeStr),
		RateDate:   time.Date(rateDay.Year(), rateDay.Month(), rateDay.Day(), 0, 0, 0, 0, time.UTC),
		RUBPerUnit: rat,
	}
	if err := res.Validate(); err != nil {
		return domain.ExchangeRate{}, ErrUnavailable
	}
	return res, nil
}
