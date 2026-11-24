package identifier_test

import (
	"testing"

	"github.com/2inxs/openbanking-service/internal/infrastructure/identifier"
	"github.com/jackc/pgx/v5/pgtype"
)

func TestGeneratedIDsAreDistinctUUIDv4(t *testing.T) {
	generator := identifier.UUIDGenerator{}
	seen := make(map[string]bool)
	for range 100 {
		id, err := generator.NewID()
		if err != nil {
			t.Fatal(err)
		}
		var parsed pgtype.UUID
		if err := parsed.Scan(id); err != nil || !parsed.Valid || parsed.Bytes[6]>>4 != 4 || parsed.Bytes[8]>>6 != 2 {
			t.Fatalf("invalid UUIDv4 %q: %v", id, err)
		}
		if seen[id] {
			t.Fatalf("duplicate UUID: %s", id)
		}
		seen[id] = true
	}
}
