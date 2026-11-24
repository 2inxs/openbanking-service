package identifier

import (
	"crypto/rand"
	"fmt"

	"github.com/2inxs/openbanking-service/internal/usecase/banksync"
)

type UUIDGenerator struct{}

var _ banksync.IDGenerator = UUIDGenerator{}

func (UUIDGenerator) NewID() (string, error) {
	var id [16]byte
	if _, err := rand.Read(id[:]); err != nil {
		return "", fmt.Errorf("generate UUID: %w", err)
	}
	id[6] = (id[6] & 0x0f) | 0x40
	id[8] = (id[8] & 0x3f) | 0x80
	return fmt.Sprintf("%x-%x-%x-%x-%x", id[:4], id[4:6], id[6:8], id[8:10], id[10:]), nil
}
