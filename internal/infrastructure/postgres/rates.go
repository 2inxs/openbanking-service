package postgres

import (
	"context"
	"math/big"
	"sort"
	"time"

	"github.com/2inxs/openbanking-service/internal/domain"
	"github.com/2inxs/openbanking-service/internal/domain/money"
	"github.com/2inxs/openbanking-service/internal/usecase/currency"
	"github.com/jackc/pgx/v5"
)

type RateRepository struct {
	db DBTX
}

var _ currency.RateRepository = (*RateRepository)(nil)

func NewRateRepository(db DBTX) *RateRepository {
	return &RateRepository{db: db}
}

type txBeginner interface {
	Begin(ctx context.Context) (pgx.Tx, error)
}

func normalizeRateDay(t time.Time) time.Time {
	u := t.UTC()
	return time.Date(u.Year(), u.Month(), u.Day(), 0, 0, 0, 0, time.UTC)
}

func (r *RateRepository) SaveBatch(ctx context.Context, daily domain.DailyRates) error {
	if daily.Date.IsZero() {
		return domain.ErrInvalidModel
	}
	expectedDay := normalizeRateDay(daily.Date)

	codes := make([]money.Currency, 0, len(daily.Rates))
	for code := range daily.Rates {
		codes = append(codes, code)
	}
	sort.Slice(codes, func(i, j int) bool { return codes[i] < codes[j] })

	// 1. Валидируем весь пакет до любых обращений к БД.
	validated := make([]domain.ExchangeRate, 0, len(codes))
	for _, code := range codes {
		rate := daily.Rates[code]
		if err := rate.Validate(); err != nil {
			return err
		}
		if rate.Currency != code {
			return domain.ErrInvalidModel
		}
		if !normalizeRateDay(rate.RateDate).Equal(expectedDay) {
			return domain.ErrInvalidModel
		}
		maxNumeric := new(big.Rat).SetInt(new(big.Int).Exp(big.NewInt(10), big.NewInt(14), nil))
		if rate.RUBPerUnit.Cmp(maxNumeric) >= 0 {
			return domain.ErrInvalidModel
		}
		validated = append(validated, rate)
	}

	if len(validated) == 0 {
		return nil
	}

	// 2. Выполняем сохранение атомарно в одной транзакции.
	if beginner, ok := r.db.(txBeginner); ok {
		tx, err := beginner.Begin(ctx)
		if err != nil {
			return repositoryError(err, currency.ErrRateNotFound, domain.ErrInvalidModel)
		}
		defer func() { _ = tx.Rollback(ctx) }()

		if err := saveRatesAtomic(ctx, tx, expectedDay, validated); err != nil {
			return err
		}
		if err := tx.Commit(ctx); err != nil {
			return repositoryError(err, currency.ErrRateNotFound, domain.ErrInvalidModel)
		}
		return nil
	}

	return saveRatesAtomic(ctx, r.db, expectedDay, validated)
}

func saveRatesAtomic(ctx context.Context, execDB DBTX, rateDate time.Time, rates []domain.ExchangeRate) error {
	// Читаем поддерживаемые валюты из справочника БД, чтобы отфильтровать
	// лишние валюты из ответа ЦБ (например, AUD) без нарушения FK и без потери атомарности.
	rows, err := execDB.Query(ctx, `SELECT code FROM currencies`)
	if err != nil {
		return repositoryError(err, currency.ErrRateNotFound, domain.ErrInvalidModel)
	}
	supported := make(map[money.Currency]struct{})
	for rows.Next() {
		var code string
		if err := rows.Scan(&code); err != nil {
			rows.Close()
			return repositoryError(err, currency.ErrRateNotFound, domain.ErrInvalidModel)
		}
		supported[money.Currency(code)] = struct{}{}
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return repositoryError(err, currency.ErrRateNotFound, domain.ErrInvalidModel)
	}
	rows.Close()

	var (
		colCodes []string
		colDates []string
		colRates []string
	)
	dateStr := rateDate.Format("2006-01-02")
	for _, rate := range rates {
		if rate.Currency == money.RUB {
			continue
		}
		if _, ok := supported[rate.Currency]; !ok {
			continue
		}
		colCodes = append(colCodes, string(rate.Currency))
		colDates = append(colDates, dateStr)
		colRates = append(colRates, rate.RUBPerUnitString())
	}

	if len(colCodes) == 0 {
		return nil
	}

	// Одиночный INSERT ... unnest гарантирует атомарную вставку/обновление всего пакета.
	_, err = execDB.Exec(ctx, `INSERT INTO exchange_rates (currency_code, rate_date, rub_per_unit, fetched_at)
 SELECT code, dt::date, rate::numeric, now()
 FROM unnest($1::varchar[], $2::text[], $3::text[]) AS t(code, dt, rate)
 ON CONFLICT (currency_code, rate_date)
 DO UPDATE SET rub_per_unit = EXCLUDED.rub_per_unit, fetched_at = EXCLUDED.fetched_at`,
		colCodes, colDates, colRates)
	if err != nil {
		return repositoryError(err, currency.ErrRateNotFound, domain.ErrInvalidModel)
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
		reqDay := normalizeRateDay(*date)
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
		RateDate:   normalizeRateDay(rateDay),
		RUBPerUnit: rat,
	}
	if err := res.Validate(); err != nil {
		return domain.ExchangeRate{}, ErrUnavailable
	}
	return res, nil
}
