package openbanking

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/2inxs/openbanking-service/internal/domain"
	"github.com/2inxs/openbanking-service/internal/usecase/banksync"
)

var (
	ErrUnauthorized     = errors.New("openbanking: unauthorized") // 401: токен не принят
	ErrUnexpectedStatus = errors.New("openbanking: unexpected HTTP status")
	ErrResponseTooLarge = errors.New("openbanking: response too large")
	ErrInvalidConfig    = errors.New("openbanking: invalid client config")
)

const (
	defaultTimeout = 10 * time.Second
	maxBodyBytes   = 2 << 20 // 2 MiB
)

type Client struct {
	baseURL    string
	token      string
	httpClient *http.Client
}

func WithHTTPClient(hc *http.Client) Option {
	return func(c *Client) {
		if hc != nil {
			c.httpClient = hc
		}
	}
}

type Option func(*Client)

func NewClient(baseURL, token string, opts ...Option) (*Client, error) {
	baseURL = strings.TrimRight(strings.TrimSpace(baseURL), "/")
	u, err := url.Parse(baseURL)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return nil, fmt.Errorf("%w: base URL %q must be http(s) with host", ErrInvalidConfig, baseURL)
	}
	token = strings.TrimSpace(token)
	if token == "" {
		return nil, fmt.Errorf("%w: empty token", ErrInvalidConfig)
	}
	c := &Client{
		baseURL:    baseURL,
		token:      token,
		httpClient: &http.Client{Timeout: defaultTimeout},
	}
	for _, opt := range opts {
		opt(c)
	}
	return c, nil
}

// get - Один метод для запроса по трем контрактам
func (c *Client) get(ctx context.Context, path string, query url.Values) ([]byte, error) {
	u := c.baseURL + path
	if len(query) > 0 {
		u += "?" + query.Encode()
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, fmt.Errorf("openbanking: build GET %s: %w", path, err)
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", "openbanking-service/1.0")
	req.Header.Set("Authorization", "Bearer "+c.token)
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("openbanking: GET %s: %w", path, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusUnauthorized {
		return nil, fmt.Errorf("%w: GET %s", ErrUnauthorized, path)
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("%w: %d on GET %s", ErrUnexpectedStatus, resp.StatusCode, path)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxBodyBytes+1))
	if err != nil {
		return nil, fmt.Errorf("openbanking: read GET %s: %w", path, err)
	}
	if len(body) > maxBodyBytes {
		return nil, fmt.Errorf("%w: GET %s", ErrResponseTooLarge, path)
	}
	return body, nil
}

func (c *Client) Balance(ctx context.Context, accountID string) (domain.BalanceSnapshot, error) {
	body, err := c.get(ctx, "/accounts/"+url.PathEscape(accountID)+"/balances", nil)
	if err != nil {
		return domain.BalanceSnapshot{}, err
	}
	return ParseBalance(body, accountID)
}

func (c *Client) Transactions(ctx context.Context, accountID string, from, to time.Time) ([]banksync.BankTransaction, error) {
	query := url.Values{}
	if !from.IsZero() {
		query.Set("fromBookingDateTime", from.Format(time.RFC3339))
	}
	if !to.IsZero() {
		query.Set("toBookingDateTime", to.Format(time.RFC3339))
	}
	body, err := c.get(ctx, "/accounts/"+url.PathEscape(accountID)+"/transactions", query)
	if err != nil {
		return nil, err
	}
	return ParseTransactions(body, accountID)
}

func (c *Client) Accounts(ctx context.Context) ([]banksync.BankAccount, error) {
	body, err := c.get(ctx, "/accounts", nil)
	if err != nil {
		return nil, err
	}
	return ParseAccounts(body)
}
