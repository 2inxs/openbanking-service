package money_test

import (
	"encoding/json"
	"errors"
	"testing"

	"github.com/2inxs/openbanking-service/internal/domain/money"
)

func amount(t *testing.T, value string) money.Amount {
	t.Helper()
	a, err := money.Parse(value)
	if err != nil {
		t.Fatal(err)
	}
	return a
}

func TestParseAndFormat(t *testing.T) {
	for input, want := range map[string]string{
		"0": "0.0000", "-0.0000": "0.0000", "001.2": "1.2000",
		"-0.0001": "-0.0001", "123.4567": "123.4567",
		"9999999999999999.9999":               "9999999999999999.9999",
		"123456789012345678901234567890.1234": "123456789012345678901234567890.1234",
	} {
		t.Run(input, func(t *testing.T) {
			if got := amount(t, input).String(); got != want {
				t.Fatalf("got %s, want %s", got, want)
			}
		})
	}
	var zero money.Amount
	if zero.String() != "0.0000" || zero.Sign() != 0 {
		t.Fatalf("invalid zero value: %s", zero)
	}
}

func TestParseRejectsAmbiguousOrInexactValues(t *testing.T) {
	for _, input := range []string{"", "-", ".5", "1.", "1.23456", "1e3", "1,50", "+1", " 1", "1 ", "1..2", "NaN", "Infinity", "١.٢"} {
		t.Run(input, func(t *testing.T) {
			if _, err := money.Parse(input); !errors.Is(err, money.ErrInvalidAmount) {
				t.Fatalf("expected invalid amount, got %v", err)
			}
		})
	}
}

func TestArithmeticIsExactAndDoesNotMutateCopies(t *testing.T) {
	a := amount(t, "9999999999999999.9999")
	copyOfA := a
	b := amount(t, "0.0001")
	if got := a.Add(b).String(); got != "10000000000000000.0000" {
		t.Fatalf("addition lost precision: %s", got)
	}
	if got := amount(t, "0.1").Add(amount(t, "0.2")).String(); got != "0.3000" {
		t.Fatalf("decimal addition is inexact: %s", got)
	}
	if got := b.Sub(a).String(); got != "-9999999999999999.9998" {
		t.Fatalf("unexpected difference: %s", got)
	}
	_ = a.Negate()
	_, _ = a.Round(0)
	if a.String() != "9999999999999999.9999" || copyOfA.Cmp(a) != 0 || b.String() != "0.0001" {
		t.Fatal("arithmetic changed an operand")
	}
	if a.Cmp(b) <= 0 || b.Negate().Sign() != -1 {
		t.Fatal("comparison or sign is incorrect")
	}
}

func TestRoundHalfAwayFromZero(t *testing.T) {
	for _, tc := range []struct {
		input  string
		places int
		want   string
	}{
		{"1.2349", 2, "1.2300"}, {"1.2350", 2, "1.2400"},
		{"-1.2349", 2, "-1.2300"}, {"-1.2350", 2, "-1.2400"},
		{"9.9999", 2, "10.0000"}, {"-0.0001", 2, "0.0000"},
		{"1.5000", 0, "2.0000"}, {"-1.5000", 0, "-2.0000"},
		{"1.2345", 4, "1.2345"},
	} {
		t.Run(tc.input+"/"+tc.want, func(t *testing.T) {
			got, err := amount(t, tc.input).Round(tc.places)
			if err != nil || got.String() != tc.want {
				t.Fatalf("got %s, %v; want %s", got, err, tc.want)
			}
		})
	}
	for _, places := range []int{-1, 5} {
		if _, err := (money.Amount{}).Round(places); !errors.Is(err, money.ErrInvalidScale) {
			t.Fatalf("expected invalid scale, got %v", err)
		}
	}
}

func TestJSONPreservesPrecisionAndRejectsNumbers(t *testing.T) {
	a := amount(t, "9999999999999999.9999")
	data, err := json.Marshal(a)
	if err != nil || string(data) != `"9999999999999999.9999"` {
		t.Fatalf("unexpected JSON: %s, %v", data, err)
	}
	var decoded money.Amount
	if err := json.Unmarshal(data, &decoded); err != nil || decoded.Cmp(a) != 0 {
		t.Fatalf("round trip lost precision: %s, %v", decoded, err)
	}
	for _, bad := range []string{`1.25`, `null`, `"1.23456"`, `true`, `{}`} {
		decoded = a
		if err := json.Unmarshal([]byte(bad), &decoded); !errors.Is(err, money.ErrInvalidAmount) {
			t.Fatalf("expected rejection of %s, got %v", bad, err)
		}
		if decoded.Cmp(a) != 0 {
			t.Fatal("failed decoding changed the receiver")
		}
	}
}
