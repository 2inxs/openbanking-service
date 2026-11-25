package domain_test

import (
	"errors"
	"testing"
	"time"

	"github.com/2inxs/openbanking-service/internal/domain"
	"github.com/2inxs/openbanking-service/internal/domain/money"
)

func mustAmt(t *testing.T, v string) money.Amount {
	t.Helper()
	a, err := money.Parse(v)
	if err != nil {
		t.Fatal(err)
	}
	return a
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
			name: "open_banking_with_external_id",
			in: domain.DeduplicationInput{
				AccountID:  accID,
				Source:     domain.SourceOpenBanking,
				ExternalID: "tx-bank-001",
				Amount:     mustAmt(t, "-450.00"),
				Currency:   money.RUB,
				BookedAt:   &booked,
			},
			wantStrategy: domain.DedupStrategyExternalID,
		},
		{
			name: "statement_with_external_id_trimmed",
			in: domain.DeduplicationInput{
				AccountID:         accID,
				Source:            domain.SourceStatement,
				ExternalID:        "  tx-bank-001  ",
				Amount:            mustAmt(t, "-450.00"),
				Currency:          money.RUB,
				BookedAt:          &booked,
				StatementImportID: "22222222-2222-2222-2222-222222222222",
				RowNumber:         12,
			},
			wantStrategy: domain.DedupStrategyExternalID,
		},
		{
			name: "statement_without_external_id_uses_line_strategy",
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
			name: "open_banking_without_external_id_rejected",
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
			name: "statement_without_external_id_and_missing_row_rejected",
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
				return
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

	if keys["open_banking_with_external_id"] != keys["statement_with_external_id_trimmed"] {
		t.Fatalf("API and statement keys for same external_id must match: api=%s stmt=%s",
			keys["open_banking_with_external_id"], keys["statement_with_external_id_trimmed"])
	}
}

func TestIdenticalRealPaymentsWithoutExternalIDAreNotCollapsed(t *testing.T) {
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
		t.Fatalf("two real identical payments without external_id must have distinct keys, got identical %s", k1)
	}
}
