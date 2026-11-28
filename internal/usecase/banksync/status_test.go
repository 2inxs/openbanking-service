package banksync

import (
	"testing"

	"github.com/2inxs/openbanking-service/internal/domain"
)

func TestCanTransition(t *testing.T) {
	tests := []struct {
		name     string
		from, to domain.ConnectionStatusID
		want     bool
	}{
		{"pending to active", domain.ConnectionPending, domain.ConnectionActive, true},
		{"active to error", domain.ConnectionActive, domain.ConnectionError, true},
		{"error to active", domain.ConnectionError, domain.ConnectionActive, true},
		{"disconnected to active", domain.ConnectionDisconnected, domain.ConnectionActive, true},
		{"same status", domain.ConnectionActive, domain.ConnectionActive, true},

		{"disconnected to error", domain.ConnectionDisconnected, domain.ConnectionError, false},
		{"back to pending", domain.ConnectionActive, domain.ConnectionPending, false},
		{"unknown status", domain.ConnectionActive, domain.ConnectionStatusID(99), false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := CanTransition(tt.from, tt.to); got != tt.want {
				t.Fatalf("CanTransition(%v, %v) = %v, want %v", tt.from, tt.to, got, tt.want)
			}
		})
	}
}
