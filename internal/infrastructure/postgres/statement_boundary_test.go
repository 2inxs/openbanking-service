package postgres

import (
	"context"
	"errors"
	"fmt"
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

func TestStatementMismatchDoesNotOpenTransaction(t *testing.T) {
	for _, tc := range []struct {
		name   string
		change func(*st.TransactionChunk)
		want   error
	}{
		{"account_number", func(c *st.TransactionChunk) { c.AccountNumber = "different" }, st.ErrAccountNumberMismatch},
		{"currency", func(c *st.TransactionChunk) { c.Currency = "USD" }, st.ErrAccountCurrencyMismatch},
	} {
		for _, invalidIndex := range []int{0, 2} {
			t.Run(fmt.Sprintf("%s/chunk_%d", tc.name, invalidIndex), func(t *testing.T) {
				db := &preparationDB{t: t}
				chunks := make(chan st.TransactionChunk, 3)
				for index := 0; index < 3; index++ {
					chunk := st.TransactionChunk{AccountNumber: "account", Currency: "RUB", Rows: []st.TransactionRow{{}}}
					if index == invalidIndex {
						tc.change(&chunk)
					}
					chunks <- chunk
				}
				close(chunks)
				_, err := NewStatementImportRepository(db).ImportStream(context.Background(), st.ImportTargetAccount{
					AccountNumber: "account", Currency: "RUB",
				}, "test.csv", chunks, func() (string, []st.RowError, error) {
					db.finalized = true
					return strings.Repeat("a", 64), nil, nil
				})
				if !errors.Is(err, tc.want) || db.began {
					t.Fatalf("mismatch opened transaction or lost error: began=%v err=%v", db.began, err)
				}
			})
		}
	}
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
