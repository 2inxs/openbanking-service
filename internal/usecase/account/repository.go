package account

import (
	"context"
	"errors"

	"github.com/2inxs/openbanking-service/internal/domain"
)

var (
	ErrNotFound = errors.New("account: not found")
	ErrConflict = errors.New("account: identity or ownership conflict")
)

type Repository interface {
	Get(context.Context, domain.UserID, domain.AccountID) (domain.Account, error)
	ListByUser(context.Context, domain.UserID) ([]domain.Account, error)
}
