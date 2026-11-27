package postgres

import (
	"context"
	"errors"
	st "github.com/2inxs/openbanking-service/internal/usecase/statement"
	"github.com/jackc/pgx/v5"
	"strings"
	"testing"
)

type preparationDB struct {
	DBTX
	finalized bool
	began     bool
	t         *testing.T
}

func (d *preparationDB) Begin(context.Context) (pgx.Tx, error) {
	d.began = true
	if !d.finalized {
		d.t.Fatal("SQL transaction began before input was fully read and validated")
	}
	return nil, errors.New("stop before actual SQL")
}
func TestStatementImportPreparesInputBeforeOpeningTransaction(t *testing.T) {
	for _, invalid := range []bool{false, true} {
		db := &preparationDB{t: t}
		repo := NewStatementImportRepository(db)
		chunks := make(chan st.TransactionChunk, 1)
		chunks <- st.TransactionChunk{Rows: []st.TransactionRow{{}}}
		close(chunks)
		_, err := repo.ImportStream(context.Background(), st.ImportTargetAccount{}, "test.csv", chunks, func() (string, []st.RowError, error) {
			db.finalized = true
			if invalid {
				return "", nil, st.ErrValidationFailed
			}
			return strings.Repeat("a", 64), nil, nil
		})
		if invalid {
			if db.began || !errors.Is(err, st.ErrValidationFailed) {
				t.Fatalf("invalid input opened transaction: began=%v err=%v", db.began, err)
			}
		} else if !db.began || !errors.Is(err, ErrUnavailable) {
			t.Fatalf("prepared input did not reach transaction: began=%v err=%v", db.began, err)
		}
	}
}
