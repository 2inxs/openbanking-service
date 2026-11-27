package statement

import (
	"context"
	"errors"
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
