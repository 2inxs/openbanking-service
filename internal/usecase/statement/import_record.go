package statement

import (
	"context"
	"errors"
	"regexp"
	"strings"

	"github.com/2inxs/openbanking-service/internal/domain"
)

type ImportStatus string

const (
	ImportPending    ImportStatus = "pending"
	ImportProcessing ImportStatus = "processing"
	ImportCompleted  ImportStatus = "completed"
	ImportFailed     ImportStatus = "failed"
)

var ErrImportConflict = errors.New("statement: duplicate import or concurrent status transition")
var ErrImportNotFound = errors.New("statement: import not found")

type ImportRecord struct {
	ID                                      domain.StatementImportID
	AccountID                               domain.AccountID
	FileName, FileSHA256                    string
	Status                                  ImportStatus
	ImportedCount, SkippedCount, ErrorCount int
	ErrorMessage                            string // Safe code/message only; never a raw bank response or secret.
}
type ImportRecords interface {
	Create(context.Context, domain.UserID, ImportRecord) (ImportRecord, error)
	Get(context.Context, domain.UserID, domain.StatementImportID) (ImportRecord, error)
	Transition(context.Context, domain.UserID, ImportRecord, ImportStatus) (ImportRecord, error)
}

var fileHashPattern = regexp.MustCompile(`^[0-9a-f]{64}$`)

func (s ImportStatus) Valid() bool {
	switch s {
	case ImportPending, ImportProcessing, ImportCompleted, ImportFailed:
		return true
	default:
		return false
	}
}

func CanTransition(from, to ImportStatus) bool {
	return (from == ImportPending && (to == ImportProcessing || to == ImportFailed)) ||
		(from == ImportProcessing && (to == ImportCompleted || to == ImportFailed))
}

func (i ImportRecord) Validate() error {
	if strings.TrimSpace(i.FileName) == "" || !fileHashPattern.MatchString(i.FileSHA256) || !i.Status.Valid() ||
		i.ImportedCount < 0 || i.SkippedCount < 0 || i.ErrorCount < 0 {
		return domain.ErrInvalidModel
	}
	if i.Status == ImportCompleted && (i.ErrorCount != 0 || i.ErrorMessage != "") {
		return domain.ErrInvalidModel
	}
	if i.Status == ImportFailed && i.ImportedCount != 0 {
		return domain.ErrInvalidModel
	}
	if (i.Status == ImportPending || i.Status == ImportProcessing) &&
		(i.ImportedCount != 0 || i.SkippedCount != 0 || i.ErrorCount != 0 || i.ErrorMessage != "") {
		return domain.ErrInvalidModel
	}
	return nil
}

func (i ImportRecord) ValidateForCreate() error {
	if err := i.Validate(); err != nil {
		return err
	}
	if (i.Status != ImportPending && i.Status != ImportFailed) || i.ImportedCount != 0 || i.SkippedCount != 0 {
		return domain.ErrInvalidModel
	}
	return nil
}
