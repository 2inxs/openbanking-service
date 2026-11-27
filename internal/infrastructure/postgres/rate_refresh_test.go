package postgres

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/2inxs/openbanking-service/internal/domain"
	"github.com/2inxs/openbanking-service/internal/usecase/currency"
)

func TestRefreshRejectsInvalidLeaseBeforeAccessingDatabase(t *testing.T) {
	valid := currency.RefreshLease{Key: "cbr:latest", Owner: "00000000-0000-4000-8000-000000000001", Version: 1}
	for _, change := range []struct {
		name   string
		mutate func(*currency.RefreshLease)
	}{
		{"empty", func(l *currency.RefreshLease) { *l = currency.RefreshLease{} }},
		{"key", func(l *currency.RefreshLease) { l.Key = "" }},
		{"owner", func(l *currency.RefreshLease) { l.Owner = "invalid" }},
		{"zero_version", func(l *currency.RefreshLease) { l.Version = 0 }},
		{"negative_version", func(l *currency.RefreshLease) { l.Version = -1 }},
	} {
		t.Run(change.name, func(t *testing.T) {
			lease := valid
			change.mutate(&lease)
			// A nil database makes any accidental SQL call fail this test.
			c := NewRateRefreshCoordinator(nil)
			if err := c.Complete(context.Background(), lease, domain.DailyRates{}, time.Hour); !errors.Is(err, domain.ErrInvalidModel) {
				t.Fatalf("Complete: %v", err)
			}
			if err := c.Fail(context.Background(), lease, time.Minute); !errors.Is(err, domain.ErrInvalidModel) {
				t.Fatalf("Fail: %v", err)
			}
		})
	}
}
