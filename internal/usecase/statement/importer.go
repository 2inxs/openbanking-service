package statement

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"strings"
	"sync"

	"github.com/2inxs/openbanking-service/internal/domain"
	"github.com/2inxs/openbanking-service/internal/domain/money"
)

var (
	ErrAccountNotFound         = errors.New("statement: account not found")
	ErrAccountNumberMismatch   = errors.New("statement: statement account number does not match target account")
	ErrAccountCurrencyMismatch = errors.New("statement: statement currency does not match target account")
)

type ImportTargetAccount struct {
	ID            domain.AccountID
	AccountNumber string
	Currency      money.Currency
}

type ImportCommand struct {
	AccountID domain.AccountID
	FileName  string
	Format    string // "csv" или "xlsx"
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
}

func NewImportService(csvParser, xlsxParser Parser, repo ImportRepository) *ImportService {
	return &ImportService{
		csvParser:  csvParser,
		xlsxParser: xlsxParser,
		repo:       repo,
	}
}

func (s *ImportService) Import(ctx context.Context, cmd ImportCommand) (ImportResult, error) {
	if strings.TrimSpace(string(cmd.AccountID)) == "" {
		return ImportResult{}, ErrAccountNotFound
	}
	if cmd.Reader == nil {
		return ImportResult{}, ErrEmptyStatement
	}

	parser, err := s.selectParser(cmd.Format, cmd.FileName)
	if err != nil {
		return ImportResult{}, err
	}

	target, err := s.repo.GetTargetAccount(ctx, cmd.AccountID)
	if err != nil {
		return ImportResult{}, err
	}

	hasher := sha256.New()
	teeReader := io.TeeReader(cmd.Reader, hasher)

	streamCtx, cancel := context.WithCancel(ctx)
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

	res, repoErr := s.repo.ImportStream(streamCtx, target, cmd.FileName, chunks, func() (string, []RowError, error) {
		wg.Wait()
		return hex.EncodeToString(hasher.Sum(nil)), rowErrors, prodErr
	})

	cancel()
	wg.Wait()

	if repoErr != nil && !errors.Is(repoErr, context.Canceled) {
		return res, repoErr
	}
	if prodErr != nil {
		res.RowErrors = rowErrors
		return res, prodErr
	}
	if repoErr != nil {
		return res, repoErr
	}
	return res, nil
}

func (s *ImportService) selectParser(format, fileName string) (Parser, error) {
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
		return s.csvParser, nil
	case "xlsx":
		return s.xlsxParser, nil
	default:
		return nil, fmt.Errorf("%w: unsupported statement format %q", ErrMalformedFile, format)
	}
}
