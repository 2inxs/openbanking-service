package domain_test

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/2inxs/openbanking-service/inte
al/domain"
	"github.com/2inxs/openbanking-service/inte
al/domain/money"
)

func mustAmt(t *testing.T, v string) money.Amount {
	t.Helper()
	a, err := money.Parse(v)
	if err != nil {
		t.Fatal(err)
	}
	retu
 a
}

func TestComputeTransactionDeduplicationKey(t *testing.T) {
	accID := domain.AccountID("11111111-1111-1111-1111-111111111111")
	booked := time.Date(2026, 11, 20, 14, 30, 0, 0, time.UTC)

	tests := []struct {
		name         string
		in           domain.DeduplicationInput
		wantStrategy domain.DeduplicationStrategy
		wantErr      error
	}{
		{
			name: "open_banking_with_exte
al_id",
			in: domain.DeduplicationInput{
				AccountID:  accID,
				Source:     domain.SourceOpenBanking,
				Exte
alID: "tx-bank-001",
				Amount:     mustAmt(t, "-450.00"),
				Currency:   money.RUB,
				BookedAt:   &booked,
			},
			wantStrategy: domain.DedupStrategyExte
alID,
		},
		{
			name: "statement_with_exte
al_id_trimmed",
			in: domain.DeduplicationInput{
				AccountID:         accID,
				Source:            domain.SourceStatement,
				Exte
alID:        "  tx-bank-001  ",
				Amount:            mustAmt(t, "-450.00"),
				Currency:          money.RUB,
				BookedAt:          &booked,
				StatementImportID: "22222222-2222-2222-2222-222222222222",
				RowNumber:         12,
			},
			wantStrategy: domain.DedupStrategyExte
alID,
		},
		{
			name: "statement_without_exte
al_id_uses_line_strategy",
			in: domain.DeduplicationInput{
				AccountID:         accID,
				Source:            domain.SourceStatement,
				Amount:            mustAmt(t, "-350.00"),
				Currency:          money.RUB,
				BookedAt:          &booked,
				StatementImportID: "22222222-2222-2222-2222-222222222222",
				RowNumber:         5,
			},
			wantStrategy: domain.DedupStrategyStatementLine,
		},
		{
			name: "open_banking_without_exte
al_id_rejected",
			in: domain.DeduplicationInput{
				AccountID: accID,
				Source:    domain.SourceOpenBanking,
				Amount:    mustAmt(t, "-350.00"),
				Currency:  money.RUB,
				BookedAt:  &booked,
			},
			wantErr: domain.ErrInsufficientDeduplicationData,
		},
		{
			name: "statement_without_exte
al_id_and_missing_row_rejected",
			in: domain.DeduplicationInput{
				AccountID: accID,
				Source:    domain.SourceStatement,
				Amount:    mustAmt(t, "-350.00"),
				Currency:  money.RUB,
				BookedAt:  &booked,
			},
			wantErr: domain.ErrInsufficientDeduplicationData,
		},
	}

	keys := make(map[string]string)
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			key, strategy, err := domain.ComputeTransactionDeduplicationKey(tc.in)
			if tc.wantErr != nil {
				if !errors.Is(err, tc.wantErr) {
					t.Fatalf("expected error %v, got %v", tc.wantErr, err)
				}
				retu

			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if strategy != tc.wantStrategy {
				t.Fatalf("strategy mismatch: got %q, want %q", strategy, tc.wantStrategy)
			}
			if len(key) != 64 {
				t.Fatalf("expected 64-char sha256 hex, got %d (%q)", len(key), key)
			}
			keys[tc.name] = key
		})
	}

	if keys["open_banking_with_exte
al_id"] != keys["statement_with_exte
al_id_trimmed"] {
		t.Fatalf("API and statement keys for same exte
al_id must match: api=%s stmt=%s",
			keys["open_banking_with_exte
al_id"], keys["statement_with_exte
al_id_trimmed"])
	}
}

func TestIdenticalRealPaymentsWithoutExte
alIDAreNotCollapsed(t *testing.T) {
	accID := domain.AccountID("11111111-1111-1111-1111-111111111111")
	impID := domain.StatementImportID("22222222-2222-2222-2222-222222222222")
	booked := time.Date(2026, 11, 21, 9, 15, 0, 0, time.UTC)
	amt := mustAmt(t, "-290.00")

	k1, _, err := domain.ComputeTransactionDeduplicationKey(domain.DeduplicationInput{
		AccountID:         accID,
		Source:            domain.SourceStatement,
		Amount:            amt,
		Currency:          money.RUB,
		BookedAt:          &booked,
		StatementImportID: impID,
		RowNumber:         1,
	})
	if err != nil {
		t.Fatal(err)
	}

	k2, _, err := domain.ComputeTransactionDeduplicationKey(domain.DeduplicationInput{
		AccountID:         accID,
		Source:            domain.SourceStatement,
		Amount:            amt,
		Currency:          money.RUB,
		BookedAt:          &booked,
		StatementImportID: impID,
		RowNumber:         2,
	})
	if err != nil {
		t.Fatal(err)
	}

	if k1 == k2 {
		t.Fatalf("two real identical payments without exte
al_id must have distinct keys, got identical %s", k1)
	}
}

func TestDeduplicationSecurityAndBoundaryCases(t *testing.T) {
accID := domain.AccountID("11111111-1111-1111-1111-111111111111")
booked := time.Date(2026, 11, 22, 11, 0, 0, 0, time.UTC)
amt := mustAmt(t, "-100.00")

// 1. Максимально допустимая длина exte
al_id = 255 проходит; 256 отклоняется с ErrInvalidModel.
maxExt := strings.Repeat("x", 255)
if _, _, err := domain.ComputeTransactionDeduplicationKey(domain.DeduplicationInput{
AccountID:  accID,
Source:     domain.SourceOpenBanking,
Exte
alID: maxExt,
Amount:     amt,
Currency:   money.RUB,
BookedAt:   &booked,
}); err != nil {
t.Fatalf("expected 255-char exte
al_id to pass, got %v", err)
}

if _, _, err := domain.ComputeTransactionDeduplicationKey(domain.DeduplicationInput{
AccountID:  accID,
Source:     domain.SourceOpenBanking,
Exte
alID: strings.Repeat("x", 256),
Amount:     amt,
Currency:   money.RUB,
BookedAt:   &booked,
}); !errors.Is(err, domain.ErrInvalidModel) {
t.Fatalf("expected ErrInvalidModel for 256-char exte
al_id, got %v", err)
}

// 2. Управляющие символы (\n, \x00) отклоняются (защита от log/delimiter injection).
if _, _, err := domain.ComputeTransactionDeduplicationKey(domain.DeduplicationInput{
AccountID:  accID,
Source:     domain.SourceOpenBanking,
Exte
alID: "tx-1\ninjected",
Amount:     amt,
Currency:   money.RUB,
BookedAt:   &booked,
}); !errors.Is(err, domain.ErrInvalidModel) {
t.Fatalf("expected ErrInvalidModel for control chars in exte
al_id, got %v", err)
}

// 3. Отсутствие обеих дат (BookedAt == nil && ValueAt == nil) отклоняется с ErrInvalidModel.
if _, _, err := domain.ComputeTransactionDeduplicationKey(domain.DeduplicationInput{
AccountID:  accID,
Source:     domain.SourceOpenBanking,
Exte
alID: "tx-no-date",
Amount:     amt,
Currency:   money.RUB,
}); !errors.Is(err, domain.ErrInvalidModel) {
t.Fatalf("expected ErrInvalidModel when both BookedAt and ValueAt are nil, got %v", err)
}

// 4. Pending-операция из API без BookedAt, но с ValueAt проходит корректно.
if _, strategy, err := domain.ComputeTransactionDeduplicationKey(domain.DeduplicationInput{
AccountID:  accID,
Source:     domain.SourceOpenBanking,
Exte
alID: "tx-pending-value-at",
Amount:     amt,
Currency:   money.RUB,
ValueAt:    &booked,
}); err != nil || strategy != domain.DedupStrategyExte
alID {
t.Fatalf("expected Pending tx with ValueAt to succeed, err=%v strategy=%q", err, strategy)
}

// 5. Отсутствие коллизий при символе '|' внутри идентификаторов.
kA, _, err := domain.ComputeTransactionDeduplicationKey(domain.DeduplicationInput{
AccountID:  "acc|ext",
Source:     domain.SourceOpenBanking,
Exte
alID: "123",
Amount:     amt,
Currency:   money.RUB,
BookedAt:   &booked,
})
if err != nil {
t.Fatal(err)
}
kB, _, err := domain.ComputeTransactionDeduplicationKey(domain.DeduplicationInput{
AccountID:  "acc",
Source:     domain.SourceOpenBanking,
Exte
alID: "ext|123",
Amount:     amt,
Currency:   money.RUB,
BookedAt:   &booked,
})
if err != nil {
t.Fatal(err)
}
if kA == kB {
t.Fatalf("delimiter collision detected: kA == kB == %s", kA)
}
}
