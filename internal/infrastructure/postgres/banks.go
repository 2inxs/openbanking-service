package postgres

import (
	"context"

	"github.com/2inxs/openbanking-service/internal/domain"
	"github.com/2inxs/openbanking-service/internal/usecase/banksync"
)

type BankRepository struct {
	db DBTX
}

var _ banksync.BankRepository = (*BankRepository)(nil)

func NewBankRepository(db DBTX) *BankRepository { return &BankRepository{db: db} }

func (r *BankRepository) GetByCode(ctx context.Context, code domain.BankCode) (domain.BankID, error) {
	var id string
	err := r.db.QueryRow(ctx, `SELECT id::text FROM banks WHERE code = $1`, string(code)).Scan(&id)
	if err != nil {
		return "", repositoryError(err, banksync.ErrBankNotFound, domain.ErrInvalidModel)
	}
	return domain.BankID(id), nil
}
