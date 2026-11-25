package currency

import (
	"context"
	"fmt"
	"math/big"
	"time"

	"github.com/2inxs/openbanking-service/internal/domain"
	"github.com/2inxs/openbanking-service/internal/domain/money"
)

type ConvertRequest struct {
	Amount money.Money
	To     money.Currency
	Date   *time.Time // nil — использовать актуальный доступный курс
	Places *int       // nil — использовать money.Scale (4 знака)
}

type ConvertResult struct {
	Source            money.Money `json:"source"`
	Target            money.Money `json:"target"`
	EffectiveRate     string      `json:"effective_rate"`
	RateDate          time.Time   `json:"rate_date"`
	FromRateDate      time.Time   `json:"from_rate_date"`
	ToRateDate        time.Time   `json:"to_rate_date"`
	ZeroAfterRounding bool        `json:"zero_after_rounding,omitempty"`
	ExactRate         *big.Rat    `json:"-"`
}

func (s *Service) Convert(ctx context.Context, req ConvertRequest) (ConvertResult, error) {
	if s == nil || s.repo == nil {
		return ConvertResult{}, fmt.Errorf("%w: currency service is not initialized", domain.ErrInvalidModel)
	}
	if err := req.Amount.Validate(); err != nil {
		return ConvertResult{}, ErrInvalidCurrency
	}
	if err := req.To.Validate(); err != nil {
		return ConvertResult{}, ErrInvalidCurrency
	}
	if req.Date != nil && req.Date.IsZero() {
		return ConvertResult{}, fmt.Errorf("%w: conversion date must not be zero", domain.ErrInvalidModel)
	}

	places := money.Scale
	if req.Places != nil {
		if *req.Places < 0 || *req.Places > money.Scale {
			return ConvertResult{}, money.ErrInvalidScale
		}
		places = *req.Places
	}

	nowMSK := s.nowFn().In(moscowLoc)
	todayMSK := time.Date(nowMSK.Year(), nowMSK.Month(), nowMSK.Day(), 0, 0, 0, 0, time.UTC)

	var normalizedDate *time.Time
	if req.Date != nil {
		reqInMSK := req.Date.In(moscowLoc)
		d := time.Date(reqInMSK.Year(), reqInMSK.Month(), reqInMSK.Day(), 0, 0, 0, 0, time.UTC)
		if d.After(todayMSK) {
			return ConvertResult{}, fmt.Errorf("%w: conversion date cannot be in the future", domain.ErrInvalidModel)
		}
		normalizedDate = &d
	}

	if req.Amount.Currency == req.To {
		d := todayMSK
		if normalizedDate != nil {
			d = *normalizedDate
		} else if s.cache != nil {
			if rateDate, _, ok := s.cache.Meta(); ok && !rateDate.IsZero() {
				d = rateDate
			}
		}
		sameRate := domain.ExchangeRate{
			Currency:   req.To,
			RateDate:   d,
			RUBPerUnit: big.NewRat(1, 1),
		}
		conv, err := domain.ConvertViaRUB(req.Amount, sameRate, sameRate, places)
		if err != nil {
			return ConvertResult{}, err
		}
		return ConvertResult{
			Source:            conv.Source,
			Target:            conv.Target,
			EffectiveRate:     conv.EffectiveRateString(),
			RateDate:          conv.RateDate,
			FromRateDate:      conv.FromRateDate,
			ToRateDate:        conv.ToRateDate,
			ZeroAfterRounding: conv.ZeroAfterRounding,
			ExactRate:         new(big.Rat).Set(conv.EffectiveRate),
		}, nil
	}

	evalDate := todayMSK
	if normalizedDate != nil {
		evalDate = *normalizedDate
	}
	fromRate, toRate, err := s.resolveConsistentRatePair(ctx, req.Amount.Currency, req.To, evalDate)
	if err != nil {
		return ConvertResult{}, err
	}

	conv, err := domain.ConvertViaRUB(req.Amount, fromRate, toRate, places)
	if err != nil {
		return ConvertResult{}, err
	}

	return ConvertResult{
		Source:            conv.Source,
		Target:            conv.Target,
		EffectiveRate:     conv.EffectiveRateString(),
		RateDate:          conv.RateDate,
		FromRateDate:      conv.FromRateDate,
		ToRateDate:        conv.ToRateDate,
		ZeroAfterRounding: conv.ZeroAfterRounding,
		ExactRate:         new(big.Rat).Set(conv.EffectiveRate),
	}, nil
}

// resolveConsistentRatePair запрашивает оба курса на единую дату оценки (evalDate),
// гарантируя полную симметричность прямого (A -> B) и обратного (B -> A) пересчёта
// независимо от порядка валют и различий в последних датах обновления отдельных валют.
func (s *Service) resolveConsistentRatePair(ctx context.Context, from, to money.Currency, evalDate time.Time) (domain.ExchangeRate, domain.ExchangeRate, error) {
	if from == money.RUB {
		toRate, err := s.GetRate(ctx, to, &evalDate)
		if err != nil {
			return domain.ExchangeRate{}, domain.ExchangeRate{}, err
		}
		fromRate := domain.ExchangeRate{
			Currency:   money.RUB,
			RateDate:   toRate.RateDate,
			RUBPerUnit: big.NewRat(1, 1),
		}
		return fromRate, toRate, nil
	}

	if to == money.RUB {
		fromRate, err := s.GetRate(ctx, from, &evalDate)
		if err != nil {
			return domain.ExchangeRate{}, domain.ExchangeRate{}, err
		}
		toRate := domain.ExchangeRate{
			Currency:   money.RUB,
			RateDate:   fromRate.RateDate,
			RUBPerUnit: big.NewRat(1, 1),
		}
		return fromRate, toRate, nil
	}

	fromRate, err := s.GetRate(ctx, from, &evalDate)
	if err != nil {
		return domain.ExchangeRate{}, domain.ExchangeRate{}, err
	}
	toRate, err := s.GetRate(ctx, to, &evalDate)
	if err != nil {
		return domain.ExchangeRate{}, domain.ExchangeRate{}, err
	}
	return fromRate, toRate, nil
}
