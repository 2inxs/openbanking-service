// Пакет cbr реализует HTTP-клиент для получения официальных курсов валют Банка России.
//
// Используемый endpoint: https://cbr.ru
// (при передаче даты добавляется query-параметр ?date_req=DD/MM/YYYY).
// Ответ отдаётся в кодировке windows-1251 и содержит фактическую дату действия
// курсов в атрибуте Date корневого элемента <ValCurs Date="DD.MM.YYYY">.
package cbr

import (
	"bytes"
	"context"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"math/big"
	"net/http"
	"net/url"
	"strings"
	"time"
	"unicode/utf8"
)

const (
	// DefaultEndpoint — официальный XML-фид ежедневных курсов валют ЦБ РФ.
	DefaultEndpoint = "https://cbr.ru"

	// RateScale задаёт точность нормализованного курса (соответствует numeric(24,10) в БД).
	RateScale = 10

	defaultTimeout = 10 * time.Second
	maxBodyBytes   = 2 << 20 // 2 MiB
)

var (
	ErrUnexpectedStatus = errors.New("cbr: unexpected HTTP status")
	ErrInvalidResponse  = errors.New("cbr: invalid XML response")
	ErrInvalidRate      = errors.New("cbr: invalid exchange rate value")
)

// Rate представляет нормализованный курс одной единицы валюты в рублях (RUB)
// на фактическую дату действия, указанную Банком России.
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

// DailyRates содержит набор курсов ЦБ РФ и фактическую дату их действия из ответа.
type DailyRates struct {
	Date  time.Time
	Rates map[money.Currency]Rate
}

// Client выполняет запросы к сервису курсов валют Банка России.
type Client struct {
	endpoint   string
	httpClient *http.Client
}

// Option настраивает клиент ЦБ РФ.
type Option func(*Client)

// WithEndpoint переопределяет URL сервиса ЦБ (удобно для тестов).
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

// NewClient создаёт клиент ЦБ РФ с настройками по умолчанию (таймаут 10s).
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

// FetchLatest запрашивает последний опубликованный набор курсов валют ЦБ РФ.
func (c *Client) FetchLatest(ctx context.Context) (DailyRates, error) {
	return c.fetch(ctx, nil)
}

// FetchByDate запрашивает курсы валют ЦБ РФ на указанную дату (?date_req=DD/MM/YYYY).
// Фактическая дата действия курсов в ответе может отличаться (например, в выходные дни).
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
	req.Header.Set("Accept", "application/xml, text/xml")

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

	return parseDailyXML(body)
}

func (c *Client) buildURL(date *time.Time) (string, error) {
	u, err := url.Parse(c.endpoint)
	if err != nil {
		return "", fmt.Errorf("cbr: invalid endpoint %q: %w", c.endpoint, err)
	}
	if date != nil {
		q := u.Query()
		q.Set("date_req", date.Format("02/01/2006"))
		u.RawQuery = q.Encode()
	}
	return u.String(), nil
}

type xmlValCurs struct {
	XMLName xml.Name    `xml:"ValCurs"`
	Date    string      `xml:"Date,attr"`
	Valutes []xmlValute `xml:"Valute"`
}

type xmlValute struct {
	CharCode string `xml:"CharCode"`
	Nominal  string `xml:"Nominal"`
	Name     string `xml:"Name"`
	Value    string `xml:"Value"`
}

func parseDailyXML(data []byte) (DailyRates, error) {
	if len(bytes.TrimSpace(data)) == 0 {
		return DailyRates{}, fmt.Errorf("%w: empty body", ErrInvalidResponse)
	}

	decoder := xml.NewDecoder(bytes.NewReader(data))
	decoder.CharsetReader = charsetReader

	var doc xmlValCurs
	if err := decoder.Decode(&doc); err != nil {
		return DailyRates{}, fmt.Errorf("%w: %v", ErrInvalidResponse, err)
	}

	dateStr := strings.TrimSpace(doc.Date)
	if dateStr == "" {
		return DailyRates{}, fmt.Errorf("%w: missing ValCurs Date attribute", ErrInvalidResponse)
	}
	rateDate, err := time.Parse("02.01.2006", dateStr)
	if err != nil {
		return DailyRates{}, fmt.Errorf("%w: invalid ValCurs Date %q: %v", ErrInvalidResponse, dateStr, err)
	}
	rateDate = time.Date(rateDate.Year(), rateDate.Month(), rateDate.Day(), 0, 0, 0, 0, time.UTC)

	if len(doc.Valutes) == 0 {
		return DailyRates{}, fmt.Errorf("%w: no currencies in response", ErrInvalidResponse)
	}

	rates := make(map[money.Currency]Rate, len(doc.Valutes)+1)
	// Базовая валюта RUB всегда имеет курс 1 к самой себе.
	rates[money.RUB] = Rate{
		Currency:   money.RUB,
		RateDate:   rateDate,
		RUBPerUnit: big.NewRat(1, 1),
	}

	for _, v := range doc.Valutes {
		code := money.Currency(strings.TrimSpace(v.CharCode))
		if err := code.Validate(); err != nil {
			// Пропускаем нестандартные или невалидные коды валют (не ISO 4217).
			continue
		}
		if code == money.RUB {
			continue
		}

		rubPerUnit, err := normalizeRate(v.Value, v.Nominal)
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
	valStr := strings.ReplaceAll(strings.TrimSpace(rawValue), ",", ".")
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

func charsetReader(charset string, input io.Reader) (io.Reader, error) {
	switch strings.ToLower(strings.TrimSpace(charset)) {
	case "utf-8", "utf8", "":
		return input, nil
	case "windows-1251", "cp1251":
		raw, err := io.ReadAll(input)
		if err != nil {
			return nil, err
		}
		return strings.NewReader(decodeWindows1251(raw)), nil
	default:
		return nil, fmt.Errorf("unsupported XML charset %q", charset)
	}
}

// win1251High отображает байты 0x80..0xFF кодировки Windows-1251 в руны Unicode.
var win1251High = [128]rune{
	0x0402, 0x0403, 0x201A, 0x0453, 0x201E, 0x2026, 0x2020, 0x2021,
	0x20AC, 0x2030, 0x0409, 0x2039, 0x040A, 0x040C, 0x040B, 0x040F,
	0x0452, 0x2018, 0x2019, 0x201C, 0x201D, 0x2022, 0x2013, 0x2014,
	utf8.RuneError, 0x2122, 0x0459, 0x203A, 0x045A, 0x045C, 0x045B, 0x045F,
	0x00A0, 0x040E, 0x045E, 0x0408, 0x00A4, 0x0490, 0x00A6, 0x00A7,
	0x0401, 0x00A9, 0x0404, 0x00AB, 0x00AC, 0x00AD, 0x00AE, 0x0407,
	0x00B0, 0x00B1, 0x0406, 0x0456, 0x0491, 0x00B5, 0x00B6, 0x00B7,
	0x0451, 0x2116, 0x0454, 0x00BB, 0x0458, 0x0405, 0x0455, 0x0457,
	0x0410, 0x0411, 0x0412, 0x0413, 0x0414, 0x0415, 0x0416, 0x0417,
	0x0418, 0x0419, 0x041A, 0x041B, 0x041C, 0x041D, 0x041E, 0x041F,
	0x0420, 0x0421, 0x0422, 0x0423, 0x0424, 0x0425, 0x0426, 0x0427,
	0x0428, 0x0429, 0x042A, 0x042B, 0x042C, 0x042D, 0x042E, 0x042F,
	0x0430, 0x0431, 0x0432, 0x0433, 0x0434, 0x0435, 0x0436, 0x0437,
	0x0438, 0x0439, 0x043A, 0x043B, 0x043C, 0x043D, 0x043E, 0x043F,
	0x0440, 0x0441, 0x0442, 0x0443, 0x0444, 0x0445, 0x0446, 0x0447,
	0x0448, 0x0449, 0x044A, 0x044B, 0x044C, 0x044D, 0x044E, 0x044F,
}

func decodeWindows1251(src []byte) string {
	var b strings.Builder
	b.Grow(len(src) * 2)
	for _, c := range src {
		if c < 0x80 {
			b.WriteByte(c)
		} else {
			b.WriteRune(win1251High[c-0x80])
		}
	}
	return b.String()
}
