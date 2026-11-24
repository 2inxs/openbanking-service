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
	"strings"
	"sync"
	"time"

	"github.com/2inxs/openbanking-service/internal/domain"
	"github.com/2inxs/openbanking-service/internal/domain/money"
)

var (
	ErrAccountNotFound         = errors.New("statement: account not found")
	ErrAccountNumberMismatch   = errors.New("statement: statement account number does not match target account")
	ErrAccountCurrencyMismatch = errors.New("statement: statement currency does not match target account")
)

type ctxLoggerKey struct{}

// WithLoggerContext кладёт обогащённый *slog.Logger в контекст для сквозной трассировки слоёв.
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
	AccountID domain.AccountID
	FileName  string
	Format    string // "csv" или "xlsx"
	TraceID   string // опциональный request_id; если пуст — ищется в ctx
	Reader    io.Reader
}

type ImportResult struct {
	ImportID      domain.StatementImportID `json:"import_id"`
	AccountID     domain.AccountID         `json:"account_id"`
	ImportedCount int                      `json:"imported_count"`
	SkippedCount  int                      `json:"skipped_count"`
	RowErrors     []RowError               `json:"row_errors,omitempty"`
}

type ImportRepository interface {
	GetTargetAccount(ctx context.Context, accountID domain.AccountID) (ImportTargetAccount, error)
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
}

func NewImportService(csvParser, xlsxParser Parser, repo ImportRepository) *ImportService {
	return &ImportService{
		csvParser:  csvParser,
		xlsxParser: xlsxParser,
		repo:       repo,
		logger:     slog.Default(),
	}
}

func (s *ImportService) WithLogger(logger *slog.Logger) *ImportService {
	if logger != nil {
		s.logger = logger
	}
	return s
}

func resolveTraceID(ctx context.Context, explicit string) string {
	if id := strings.TrimSpace(explicit); id != "" {
		return id
	}
	if ctx == nil {
		return ""
	}
	for _, key := range []string{"request_id", "trace_id", "X-Request-ID"} {
		if v, ok := ctx.Value(key).(string); ok && strings.TrimSpace(v) != "" {
			return strings.TrimSpace(v)
		}
	}
	return ""
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
	traceID := resolveTraceID(ctx, cmd.TraceID)
	safeFileName := sanitizeLogFileName(cmd.FileName)

	log := s.logger.With(
		slog.String("trace_id", traceID),
		slog.String("account_id", string(cmd.AccountID)),
		slog.String("file_name", safeFileName),
	)
	tracedCtx := WithLoggerContext(ctx, log)

	if strings.TrimSpace(string(cmd.AccountID)) == "" {
		return ImportResult{}, ErrAccountNotFound
	}
	if cmd.Reader == nil {
		return ImportResult{}, ErrEmptyStatement
	}

	parser, format, err := s.selectParser(cmd.Format, safeFileName)
	if err != nil {
		log.WarnContext(tracedCtx, "statement import rejected: unsupported format", slog.String("error", err.Error()))
		return ImportResult{}, err
	}

	target, err := s.repo.GetTargetAccount(tracedCtx, cmd.AccountID)
	if err != nil {
		log.WarnContext(tracedCtx, "statement import failed: target account lookup", slog.String("error", err.Error()))
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
		return res, repoErr
	}
	if prodErr != nil {
		res.RowErrors = rowErrors
		log.WarnContext(tracedCtx, "statement import failed during parsing",
			slog.Int("error_count", len(rowErrors)),
			slog.Int64("duration_ms", durationMs),
			slog.String("error", prodErr.Error()),
		)
		return res, prodErr
	}
	if repoErr != nil {
		log.WarnContext(tracedCtx, "statement import canceled",
			slog.Int64("duration_ms", durationMs),
			slog.String("error", repoErr.Error()),
		)
		return res, repoErr
	}

	log.InfoContext(tracedCtx, "statement import completed",
		slog.String("import_id", string(res.ImportID)),
		slog.String("file_sha256", computedSHA),
		slog.Int("imported_count", res.ImportedCount),
		slog.Int("skipped_count", res.SkippedCount),
		slog.Int64("duration_ms", durationMs),
	)
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
