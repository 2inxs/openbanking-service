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
	if err := req.Amount.Validate(); err != nil {
		return ConvertResult{}, fmt.Errorf("%w: %v", ErrInvalidCurrency, err)
	}
	if err := req.To.Validate(); err != nil {
		return ConvertResult{}, fmt.Errorf("%w: %v", ErrInvalidCurrency, err)
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

	var normalizedDate *time.Time
	if req.Date != nil {
		d := time.Date(req.Date.Year(), req.Date.Month(), req.Date.Day(), 0, 0, 0, 0, time.UTC)
		normalizedDate = &d
	}

	if req.Amount.Currency == req.To {
		d := s.nowFn().UTC().Truncate(24 * time.Hour)
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

	fromRate, err := s.GetRate(ctx, req.Amount.Currency, normalizedDate)
	if err != nil {
		return ConvertResult{}, err
	}
	toRate, err := s.GetRate(ctx, req.To, normalizedDate)
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
