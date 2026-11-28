package statement

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/2inxs/openbanking-service/internal/domain"
	"github.com/2inxs/openbanking-service/internal/domain/money"
	"github.com/2inxs/openbanking-service/internal/usecase"
	"github.com/2inxs/openbanking-service/pkg/logger"
)

var (
	ErrAccountNotFound         = errors.New("statement: account not found")
	ErrAccountNumberMismatch   = errors.New("statement: statement account number does not match target account")
	ErrAccountCurrencyMismatch = errors.New("statement: statement currency does not match target account")
)

type ctxLoggerKey struct{}

// WithLoggerContext кладёт настроенный *slog.Logger в контекст для сквозной трассировки слоёв.
func WithLoggerContext(ctx context.Context, l *slog.Logger) context.Context {
	if l == nil {
		return ctx
	}
	return context.WithValue(ctx, ctxLoggerKey{}, l)
}

// LoggerFromContext извлекает логгер трассировки из контекста (или возвращает slog.Default()).
func LoggerFromContext(ctx context.Context) *slog.Logger {
	if ctx != nil {
		if l, ok := ctx.Value(ctxLoggerKey{}).(*slog.Logger); ok && l != nil {
			return l
		}
	}
	return slog.Default()
}

type ImportTargetAccount struct {
	ID            domain.AccountID
	AccountNumber string
	Currency      money.Currency
}

type ImportCommand struct {
	UserID    domain.UserID // доверенный владелец счёта, определённый сервером (например, из сессии или DEMO_USER_ID)
	AccountID domain.AccountID
	FileName  string
	Format    string // "csv" или "xlsx"
	TraceID   string // опциональный trace_id; request_id также автоматически читается из ctx через pkg/logger
	Reader    io.Reader
}

type ImportResult struct {
	ImportID      domain.StatementImportID
	AccountID     domain.AccountID
	ImportedCount int
	SkippedCount  int
	RowErrors     []RowError
}

type ImportRepository interface {
	GetTargetAccount(ctx context.Context, userID domain.UserID, accountID domain.AccountID) (ImportTargetAccount, error)
	ImportStream(
		ctx context.Context,
		target ImportTargetAccount,
		fileName string,
		chunks <-chan TransactionChunk,
		finalize func() (fileSHA256 string, rowErrs []RowError, streamErr error),
	) (ImportResult, error)
}

type ImportService struct {
	csvParser  Parser
	xlsxParser Parser
	repo       ImportRepository
	logger     *slog.Logger
	audit      *usecase.AuditRecorder
}

func NewImportService(csvParser, xlsxParser Parser, repo ImportRepository) *ImportService {
	return &ImportService{
		csvParser:  csvParser,
		xlsxParser: xlsxParser,
		repo:       repo,
		logger:     slog.Default(),
	}
}

func (s *ImportService) WithLogger(l *slog.Logger) *ImportService {
	if l != nil {
		s.logger = l
	}
	return s
}

func (s *ImportService) WithAudit(a *usecase.AuditRecorder) *ImportService {
	s.audit = a
	return s
}

func sanitizeLogFileName(name string) string {
	clean := filepath.Base(strings.TrimSpace(name))
	if clean == "." || clean == string(filepath.Separator) {
		return ""
	}
	clean = strings.Map(func(r rune) rune {
		if r < 0x20 || r == 0x7f {
			return -1
		}
		return r
	}, clean)
	if len(clean) > 255 {
		clean = clean[:255]
	}
	return clean
}

func (s *ImportService) Import(ctx context.Context, cmd ImportCommand) (ImportResult, error) {
	startedAt := time.Now()
	safeFileName := sanitizeLogFileName(cmd.FileName)
	userIDStr := strings.TrimSpace(string(cmd.UserID))
	cmd.UserID = domain.UserID(userIDStr)
	cmd.AccountID = domain.AccountID(strings.TrimSpace(string(cmd.AccountID)))

	tracedCtx := logger.WithFields(ctx, func(f *logger.ContextFields) {
		if f.Job == "" {
			f.Job = "statement_import"
		}
		if f.UserID == "" && userIDStr != "" {
			f.UserID = userIDStr
		}
		if id := strings.TrimSpace(cmd.TraceID); id != "" && f.TraceID == "" {
			f.TraceID = id
		}
	})
	log := s.logger.With(
		slog.String("account_id", string(cmd.AccountID)),
		slog.String("file_name", safeFileName),
	)
	tracedCtx = WithLoggerContext(tracedCtx, log)

	recordAudit := func(status domain.AuditStatus, resourceID string, meta map[string]string) {
		if s.audit == nil {
			return
		}
		fields := logger.FieldsFromContext(tracedCtx)
		auditUserID := userIDStr
		if auditUserID == "" {
			auditUserID = fields.UserID
		}
		meta["account_id"] = string(cmd.AccountID)
		if safeFileName != "" {
			meta["file_name"] = safeFileName
		}
		s.audit.BestEffortLog(tracedCtx, domain.AuditEvent{
			UserID:     auditUserID,
			Action:     domain.AuditActionStatementImported,
			Status:     status,
			RequestID:  fields.RequestID,
			ResourceID: resourceID,
			Metadata:   meta,
		})
	}

	if userIDStr == "" || strings.TrimSpace(string(cmd.AccountID)) == "" {
		recordAudit(domain.AuditStatusFailure, string(cmd.AccountID), map[string]string{"reason": "missing_user_or_account"})
		return ImportResult{}, ErrAccountNotFound
	}
	if cmd.Reader == nil {
		recordAudit(domain.AuditStatusFailure, string(cmd.AccountID), map[string]string{"reason": "empty_statement"})
		return ImportResult{}, ErrEmptyStatement
	}

	parser, format, err := s.selectParser(cmd.Format, safeFileName)
	if err != nil {
		log.WarnContext(tracedCtx, "statement import rejected: unsupported format", slog.String("error", err.Error()))
		recordAudit(domain.AuditStatusFailure, string(cmd.AccountID), map[string]string{"reason": "unsupported_format"})
		return ImportResult{}, err
	}

	target, err := s.repo.GetTargetAccount(tracedCtx, cmd.UserID, cmd.AccountID)
	if err != nil {
		log.WarnContext(tracedCtx, "statement import failed: target account lookup or ownership check", slog.String("error", err.Error()))
		recordAudit(domain.AuditStatusFailure, string(cmd.AccountID), map[string]string{"format": format, "reason": "account_not_found_or_forbidden"})
		return ImportResult{}, err
	}

	log.InfoContext(tracedCtx, "statement import started",
		slog.String("format", format),
		slog.String("currency", string(target.Currency)),
	)

	hasher := sha256.New()
	teeReader := io.TeeReader(cmd.Reader, hasher)

	streamCtx, cancel := context.WithCancel(tracedCtx)
	defer cancel()

	chunks := make(chan TransactionChunk, 8)

	var (
		wg        sync.WaitGroup
		rowErrors []RowError
		prodErr   error
	)

	wg.Add(1)
	go func() {
		defer wg.Done()
		if sp, ok := parser.(StreamParser); ok {
			rowErrors, prodErr = sp.ParseStream(streamCtx, teeReader, chunks)
			return
		}
		defer close(chunks)
		parsed, err := parser.Parse(teeReader)
		rowErrors = parsed.RowErrors
		prodErr = err
		if err != nil {
			return
		}
		for i := 0; i < len(parsed.Transactions); i += DefaultChunkSize {
			end := i + DefaultChunkSize
			if end > len(parsed.Transactions) {
				end = len(parsed.Transactions)
			}
			select {
			case chunks <- TransactionChunk{
				AccountNumber: parsed.AccountNumber,
				Currency:      parsed.Currency,
				Rows:          parsed.Transactions[i:end],
			}:
			case <-streamCtx.Done():
				prodErr = streamCtx.Err()
				return
			}
		}
	}()

	var computedSHA string
	res, repoErr := s.repo.ImportStream(streamCtx, target, safeFileName, chunks, func() (string, []RowError, error) {
		wg.Wait()
		computedSHA = hex.EncodeToString(hasher.Sum(nil))
		return computedSHA, rowErrors, prodErr
	})

	cancel()
	wg.Wait()

	durationMs := time.Since(startedAt).Milliseconds()

	if repoErr != nil && !errors.Is(repoErr, context.Canceled) {
		log.WarnContext(tracedCtx, "statement import failed in repository",
			slog.Int64("duration_ms", durationMs),
			slog.String("error", repoErr.Error()),
		)
		recordAudit(domain.AuditStatusFailure, string(cmd.AccountID), map[string]string{
			"format": format,
			"reason": "repository_error",
		})
		return res, repoErr
	}
	if prodErr != nil {
		res.RowErrors = rowErrors
		log.WarnContext(tracedCtx, "statement import failed during parsing",
			slog.Int("error_count", len(rowErrors)),
			slog.Int64("duration_ms", durationMs),
			slog.String("error", prodErr.Error()),
		)
		recordAudit(domain.AuditStatusFailure, string(cmd.AccountID), map[string]string{
			"format":      format,
			"error_count": strconv.Itoa(len(rowErrors)),
			"reason":      "parse_or_validation_failed",
		})
		return res, prodErr
	}
	if repoErr != nil {
		log.WarnContext(tracedCtx, "statement import canceled",
			slog.Int64("duration_ms", durationMs),
			slog.String("error", repoErr.Error()),
		)
		recordAudit(domain.AuditStatusFailure, string(cmd.AccountID), map[string]string{
			"format": format,
			"reason": "canceled",
		})
		return res, repoErr
	}

	log.InfoContext(tracedCtx, "statement import completed",
		slog.String("import_id", string(res.ImportID)),
		slog.String("file_sha256", computedSHA),
		slog.Int("imported_count", res.ImportedCount),
		slog.Int("skipped_count", res.SkippedCount),
		slog.Int64("duration_ms", durationMs),
	)
	recordAudit(domain.AuditStatusSuccess, string(res.ImportID), map[string]string{
		"format":         format,
		"file_sha256":    computedSHA,
		"imported_count": strconv.Itoa(res.ImportedCount),
		"skipped_count":  strconv.Itoa(res.SkippedCount),
	})
	return res, nil
}

func (s *ImportService) selectParser(format, fileName string) (Parser, string, error) {
	f := strings.ToLower(strings.TrimSpace(format))
	if f == "" {
		lowerName := strings.ToLower(strings.TrimSpace(fileName))
		switch {
		case strings.HasSuffix(lowerName, ".csv"):
			f = "csv"
		case strings.HasSuffix(lowerName, ".xlsx"):
			f = "xlsx"
		}
	}
	switch f {
	case "csv":
		return s.csvParser, f, nil
	case "xlsx":
		return s.xlsxParser, f, nil
	default:
		return nil, "", fmt.Errorf("%w: unsupported statement format %q", ErrMalformedFile, format)
	}
}
