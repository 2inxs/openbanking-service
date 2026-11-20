// Пакет cbr реализует HTTP-клиент для получения курсов валют Банка России
// через сторонний JSON-сервис зеркалирования данных ЦБ РФ: cbr-xml-daily.ru.
//
// Актуальные курсы: https://cbr-xml-daily.ru
// Архивные курсы на дату: https://cbr-xml-daily.ru
// Ответ отдаётся в кодировке UTF-8 и содержит фактическую дату действия курсов в поле Date.
package cbr

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/big"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/2inxs/openbanking-service/internal/domain/money"
)

const (
	// DefaultEndpoint — JSON-фид ежедневных курсов валют ЦБ РФ от провайдера cbr-xml-daily.ru.
	DefaultEndpoint = "https://cbr-xml-daily.ru"

	// RateScale задаёт точность нормализованного курса (соответствует numeric(24,10) в БД).
	RateScale = 10

	defaultTimeout = 10 * time.Second
	maxBodyBytes   = 2 << 20 // 2 MiB
)

var (
	ErrUnexpectedStatus = errors.New("cbr: unexpected HTTP status")
	ErrInvalidResponse  = errors.New("cbr: invalid JSON response")
	ErrInvalidRate      = errors.New("cbr: invalid exchange rate value")
	ErrUnknownCurrency  = errors.New("cbr: unknown currency")
)

// Rate представляет нормализованный курс одной единицы валюты в рублях (RUB)
// на фактическую дату действия, указанную в фиде ЦБ РФ.
type Rate struct {
	Currency   money.Currency
	RateDate   time.Time
	RUBPerUnit *big.Rat
}

// RUBPerUnitString возвращает курс за 1 единицу валюты в виде точной десятичной строки
// с фиксированным масштабом RateScale (10 дробных знаков), пригодной для numeric(24,10).
func (r Rate) RUBPerUnitString() string {
	if r.RUBPerUnit == nil {
		return "0.0000000000"
	}
	return r.RUBPerUnit.FloatString(RateScale)
}

// DailyRates содержит набор курсов и фактическую дату их действия из JSON-ответа.
type DailyRates struct {
	Date  time.Time
	Rates map[money.Currency]Rate
}

// RateFor возвращает нормализованный курс для указанной валюты.
// Проверяет корректность кода валюты и её наличие в полученном справочнике.
func (d DailyRates) RateFor(currency money.Currency) (Rate, error) {
	if err := currency.Validate(); err != nil {
		return Rate{}, fmt.Errorf("%w %q: %v", ErrUnknownCurrency, currency, err)
	}
	if currency == money.RUB {
		return Rate{
			Currency:   money.RUB,
			RateDate:   d.Date,
			RUBPerUnit: big.NewRat(1, 1),
		}, nil
	}
	rate, ok := d.Rates[currency]
	if !ok || rate.RUBPerUnit == nil || rate.RUBPerUnit.Sign() <= 0 {
		return Rate{}, fmt.Errorf("%w: %s", ErrUnknownCurrency, currency)
	}
	return rate, nil
}

// Client выполняет HTTP-запросы к JSON API cbr-xml-daily.ru.
type Client struct {
	endpoint   string
	httpClient *http.Client
}

// Option настраивает клиент курсов валют.
type Option func(*Client)

// WithEndpoint переопределяет URL JSON-фида (удобно для тестов).
func WithEndpoint(endpoint string) Option {
	return func(c *Client) {
		if strings.TrimSpace(endpoint) != "" {
			c.endpoint = strings.TrimSpace(endpoint)
		}
	}
}

// WithHTTPClient задаёт пользовательский *http.Client.
func WithHTTPClient(hc *http.Client) Option {
	return func(c *Client) {
		if hc != nil {
			c.httpClient = hc
		}
	}
}

// NewClient создаёт клиент cbr-xml-daily.ru с настройками по умолчанию (таймаут 10s).
func NewClient(opts ...Option) *Client {
	c := &Client{
		endpoint: DefaultEndpoint,
		httpClient: &http.Client{
			Timeout: defaultTimeout,
		},
	}
	for _, opt := range opts {
		opt(c)
	}
	return c
}

// FetchLatest запрашивает последний опубликованный набор курсов валют в формате JSON.
func (c *Client) FetchLatest(ctx context.Context) (DailyRates, error) {
	return c.fetch(ctx, nil)
}

// FetchByDate запрашивает архивный JSON-файл курсов на указанную дату
// (/archive/YYYY/MM/DD/daily_json.js). Фактическая дата действия курсов берётся из поля Date ответа.
func (c *Client) FetchByDate(ctx context.Context, date time.Time) (DailyRates, error) {
	if date.IsZero() {
		return DailyRates{}, fmt.Errorf("%w: request date is zero", ErrInvalidResponse)
	}
	return c.fetch(ctx, &date)
}

func (c *Client) fetch(ctx context.Context, date *time.Time) (DailyRates, error) {
	reqURL, err := c.buildURL(date)
	if err != nil {
		return DailyRates{}, err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, reqURL, nil)
	if err != nil {
		return DailyRates{}, fmt.Errorf("cbr: build request: %w", err)
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", "openbanking-service/1.0")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return DailyRates{}, fmt.Errorf("cbr: execute request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return DailyRates{}, fmt.Errorf("%w: %d", ErrUnexpectedStatus, resp.StatusCode)
	}

	body, err := io.ReadAll(io.LimitReader(resp.Body, maxBodyBytes+1))
	if err != nil {
		return DailyRates{}, fmt.Errorf("cbr: read body: %w", err)
	}
	if len(body) > maxBodyBytes {
		return DailyRates{}, fmt.Errorf("%w: response exceeds %d bytes", ErrInvalidResponse, maxBodyBytes)
	}

	return parseDailyJSON(body)
}

func (c *Client) buildURL(date *time.Time) (string, error) {
	u, err := url.Parse(c.endpoint)
	if err != nil {
		return "", fmt.Errorf("cbr: invalid endpoint %q: %w", c.endpoint, err)
	}
	if date != nil {
		archivePath := fmt.Sprintf("/archive/%04d/%02d/%02d/daily_json.js", date.Year(), date.Month(), date.Day())
		u.Path = strings.TrimSuffix(u.Path, "/daily_json.js") + archivePath
	}
	return u.String(), nil
}

type jsonDailyResponse struct {
	Date   string                       `json:"Date"`
	Valute map[string]jsonValutePayload `json:"Valute"`
}

type jsonValutePayload struct {
	CharCode string      `json:"CharCode"`
	Nominal  json.Number `json:"Nominal"`
	Name     string      `json:"Name"`
	Value    json.Number `json:"Value"`
}

func parseDailyJSON(data []byte) (DailyRates, error) {
	if len(bytes.TrimSpace(data)) == 0 {
		return DailyRates{}, fmt.Errorf("%w: empty body", ErrInvalidResponse)
	}

	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()

	var doc jsonDailyResponse
	if err := decoder.Decode(&doc); err != nil {
		return DailyRates{}, fmt.Errorf("%w: %v", ErrInvalidResponse, err)
	}

	dateStr := strings.TrimSpace(doc.Date)
	if dateStr == "" {
		return DailyRates{}, fmt.Errorf("%w: missing Date field", ErrInvalidResponse)
	}
	parsedDate, err := time.Parse(time.RFC3339, dateStr)
	if err != nil {
		return DailyRates{}, fmt.Errorf("%w: invalid Date %q: %v", ErrInvalidResponse, dateStr, err)
	}
	rateDate := time.Date(parsedDate.Year(), parsedDate.Month(), parsedDate.Day(), 0, 0, 0, 0, time.UTC)

	if len(doc.Valute) == 0 {
		return DailyRates{}, fmt.Errorf("%w: no currencies in response", ErrInvalidResponse)
	}

	rates := make(map[money.Currency]Rate, len(doc.Valute)+1)
	// Базовая валюта RUB всегда имеет курс 1 к самой себе.
	rates[money.RUB] = Rate{
		Currency:   money.RUB,
		RateDate:   rateDate,
		RUBPerUnit: big.NewRat(1, 1),
	}

	for key, v := range doc.Valute {
		rawCode := strings.TrimSpace(v.CharCode)
		if rawCode == "" {
			rawCode = strings.TrimSpace(key)
		}
		code := money.Currency(rawCode)
		if err := code.Validate(); err != nil {
			continue
		}
		if code == money.RUB {
			continue
		}

		rubPerUnit, err := normalizeRate(v.Value.String(), v.Nominal.String())
		if err != nil {
			return DailyRates{}, fmt.Errorf("%w for %s: %v", ErrInvalidRate, code, err)
		}

		rates[code] = Rate{
			Currency:   code,
			RateDate:   rateDate,
			RUBPerUnit: rubPerUnit,
		}
	}

	return DailyRates{
		Date:  rateDate,
		Rates: rates,
	}, nil
}

// normalizeRate вычисляет точный курс (value / nominal) в рублях за 1 единицу валюты без float.
func normalizeRate(rawValue, rawNominal string) (*big.Rat, error) {
	valStr := strings.TrimSpace(rawValue)
	if valStr == "" {
		return nil, errors.New("empty value")
	}
	valRat, ok := new(big.Rat).SetString(valStr)
	if !ok || valRat.Sign() <= 0 {
		return nil, fmt.Errorf("invalid positive rate value %q", rawValue)
	}

	nomStr := strings.TrimSpace(rawNominal)
	if nomStr == "" {
		return nil, errors.New("empty nominal")
	}
	nomInt, ok := new(big.Int).SetString(nomStr, 10)
	if !ok || nomInt.Sign() <= 0 {
		return nil, fmt.Errorf("invalid positive nominal %q", rawNominal)
	}

	nomRat := new(big.Rat).SetInt(nomInt)
	return new(big.Rat).Quo(valRat, nomRat), nil
}
