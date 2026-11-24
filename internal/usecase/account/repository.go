package account

import (
	"context"

	"github.com/2inxs/openbanking-service/internal/domain"
)

type Repository interface {
	Get(context.Context, domain.UserID, domain.AccountID) (domain.Account, error)
	ListByUser(context.Context, domain.UserID) ([]domain.Account, error)
}
