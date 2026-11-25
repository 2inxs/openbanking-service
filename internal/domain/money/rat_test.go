package money_test

import (
	"errors"
	"math/big"
	"testing"

	"github.com/2inxs/openbanking-service/internal/domain/money"
)

func TestFromRatAndRatRoundTrip(t *testing.T) {
	tests := []struct {
		name   string
		rat    *big.Rat
		places int
		want   string
	}{
		{"zero", big.NewRat(0, 1), 4, "0.0000"},
		{"exact_four_places", big.NewRat(12345, 10000), 4, "1.2345"},
		{"half_away_positive_2", big.NewRat(247, 200), 2, "1.2400"},
		{"half_away_negative_2", big.NewRat(-247, 200), 2, "-1.2400"},
		{"below_half_positive_2", big.NewRat(12349, 10000), 2, "1.2300"},
		{"half_away_integer_0", big.NewRat(3, 2), 0, "2.0000"},
		{"negative_half_integer_0", big.NewRat(-3, 2), 0, "-2.0000"},
		{"repeating_third_4", big.NewRat(1, 3), 4, "0.3333"},
		{"repeating_two_thirds_4", big.NewRat(2, 3), 4, "0.6667"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := money.FromRat(tc.rat, tc.places)
			if err != nil {
				t.Fatalf("FromRat(%v, %d) unexpected error: %v", tc.rat, tc.places, err)
			}
			if got.String() != tc.want {
				t.Fatalf("FromRat(%v, %d) = %s, want %s", tc.rat, tc.places, got.String(), tc.want)
			}
		})
	}

	if _, err := money.FromRat(nil, 2); !errors.Is(err, money.ErrInvalidAmount) {
		t.Fatalf("expected ErrInvalidAmount for nil rat, got %v", err)
	}
	if _, err := money.FromRat(big.NewRat(1, 1), 5); !errors.Is(err, money.ErrInvalidScale) {
		t.Fatalf("expected ErrInvalidScale for places=5, got %v", err)
	}

	orig := amount(t, "98.7654")
	ratCopy := orig.Rat()
	ratCopy.SetInt64(1)
	if orig.String() != "98.7654" {
		t.Fatalf("Rat() mutated original Amount: %s", orig.String())
	}
}
