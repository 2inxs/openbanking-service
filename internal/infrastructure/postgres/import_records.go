package postgres

import (
	"context"
	"errors"
	"github.com/2inxs/openbanking-service/internal/domain"
	st "github.com/2inxs/openbanking-service/internal/usecase/statement"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

type ImportRecordRepository struct{ db DBTX }

var _ st.ImportRecords = (*ImportRecordRepository)(nil)

func NewImportRecordRepository(db DBTX) *ImportRecordRepository {
	return &ImportRecordRepository{db: db}
}

const importRecordColumns = `i.id::text,i.account_id::text,i.file_name,i.file_sha256,i.status,i.imported_count,i.skipped_count,i.error_count,COALESCE(i.error_message,'')`

func importError(err error) error {
	var pe *pgconn.PgError
	if errors.As(err, &pe) && pe.Code == "23505" && pe.ConstraintName == "statement_imports_file_uq" {
		return st.ErrImportConflict
	}
	return repositoryError(err, st.ErrImportNotFound, domain.ErrInvalidModel)
}
func scanImport(row pgx.Row) (st.ImportRecord, error) {
	var i st.ImportRecord
	err := row.Scan(&i.ID, &i.AccountID, &i.FileName, &i.FileSHA256, &i.Status, &i.ImportedCount, &i.SkippedCount, &i.ErrorCount, &i.ErrorMessage)
	return i, importError(err)
}
func (r *ImportRecordRepository) Create(ctx context.Context, user domain.UserID, i st.ImportRecord) (st.ImportRecord, error) {
	if err := validIDs(string(user), string(i.ID), string(i.AccountID)); err != nil {
		return st.ImportRecord{}, err
	}
	if err := i.ValidateForCreate(); err != nil {
		return st.ImportRecord{}, err
	}
	return scanImport(r.db.QueryRow(ctx, `INSERT INTO statement_imports AS i
 (id,account_id,file_name,file_sha256,status,error_count,error_message,completed_at)
 SELECT $1,$2,$3,$4,$5,$6,NULLIF($7,''),CASE WHEN $5='failed' THEN now() END
 FROM accounts a WHERE a.id=$2 AND a.user_id=$8 RETURNING `+importRecordColumns,
		string(i.ID), string(i.AccountID), i.FileName, i.FileSHA256, string(i.Status), i.ErrorCount, i.ErrorMessage, string(user)))
}
func (r *ImportRecordRepository) Get(ctx context.Context, user domain.UserID, id domain.StatementImportID) (st.ImportRecord, error) {
	if err := validIDs(string(user), string(id)); err != nil {
		return st.ImportRecord{}, err
	}
	return scanImport(r.db.QueryRow(ctx, `SELECT `+importRecordColumns+` FROM statement_imports i JOIN accounts a ON a.id=i.account_id WHERE i.id=$1 AND a.user_id=$2`, string(id), string(user)))
}
func (r *ImportRecordRepository) Transition(ctx context.Context, user domain.UserID, i st.ImportRecord, expected st.ImportStatus) (st.ImportRecord, error) {
	if err := validIDs(string(user), string(i.ID), string(i.AccountID)); err != nil {
		return st.ImportRecord{}, err
	}
	if err := i.Validate(); err != nil {
		return st.ImportRecord{}, err
	}
	if !st.CanTransition(expected, i.Status) {
		return st.ImportRecord{}, domain.ErrInvalidModel
	}
	got, err := scanImport(r.db.QueryRow(ctx, `UPDATE statement_imports AS i SET status=$4,imported_count=$5,skipped_count=$6,error_count=$7,error_message=NULLIF($8,''),
 completed_at=CASE WHEN $4 IN ('completed','failed') THEN now() ELSE NULL END
 FROM accounts a WHERE i.id=$1 AND i.account_id=$2 AND a.id=i.account_id AND a.user_id=$3 AND i.status=$9 RETURNING `+importRecordColumns,
		string(i.ID), string(i.AccountID), string(user), string(i.Status), i.ImportedCount, i.SkippedCount, i.ErrorCount, i.ErrorMessage, string(expected)))
	if errors.Is(err, st.ErrImportNotFound) {
		existing, getErr := r.Get(ctx, user, i.ID)
		if getErr != nil {
			return st.ImportRecord{}, getErr
		}
		if existing.AccountID != i.AccountID {
			return st.ImportRecord{}, st.ErrImportNotFound
		}
		return st.ImportRecord{}, st.ErrImportConflict
	}
	return got, err
}
