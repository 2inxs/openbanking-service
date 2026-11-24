package statement_test

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/2inxs/openbanking-service/internal/domain"
	"github.com/2inxs/openbanking-service/internal/domain/money"
	"github.com/2inxs/openbanking-service/internal/infrastructure/memory"
	"github.com/2inxs/openbanking-service/internal/infrastructure/statement"
	"github.com/2inxs/openbanking-service/internal/usecase"
	usecasestmt "github.com/2inxs/openbanking-service/internal/usecase/statement"
	"github.com/2inxs/openbanking-service/pkg/logger"
)

type stubImportRepo struct {
	target usecasestmt.ImportTargetAccount
}

func (s stubImportRepo) GetTargetAccount(_ context.Context, _ domain.AccountID) (usecasestmt.ImportTargetAccount, error) {
	return s.target, nil
}
func (s stubImportRepo) ImportStream(
	ctx context.Context,
	target usecasestmt.ImportTargetAccount,
	_ string,
	chunks <-chan usecasestmt.TransactionChunk,
	finalize func() (string, []usecasestmt.RowError, error),
) (usecasestmt.ImportResult, error) {
	var total int
	for ch := range chunks {
		total += len(ch.Rows)
	}
	_, rowErrs, err := finalize()
	if err != nil {
		return usecasestmt.ImportResult{AccountID: target.ID, RowErrors: rowErrs}, err
	}
	return usecasestmt.ImportResult{
		ImportID:      "imp-123",
		AccountID:     target.ID,
		ImportedCount: total,
	}, nil
}
func TestImportServiceRecordsTechLogsAndAuditEvent(t *testing.T) {
	var logBuf bytes.Buffer
	techLog, err := logger.New("debug", &logBuf)
	if err != nil {
		t.Fatal(err)
	}
	auditRepo := memory.NewAuditRepository(techLog)
	auditRecorder := usecase.NewAuditRecorder(auditRepo, nil, techLog)
	repo := stubImportRepo{
		target: usecasestmt.ImportTargetAccount{
			ID:            "acc-uuid-1",
			AccountNumber: "40817810000000000001",
			Currency:      money.RUB,
		},
	}
	svc := usecasestmt.NewImportService(statement.NewCSVParser(), statement.NewXLSXParser(), repo).
		WithLogger(techLog).
		WithAudit(auditRecorder)
	ctx := logger.WithFields(context.Background(), func(f *logger.ContextFields) {
		f.RequestID = "req-trace-99"
		f.UserID = "user-uuid-7"
	})
	csvData := "account_number;booked_at;amount;currency;external_id;description;counterparty\n" +
		"40817810000000000001;2026-11-20;-100.00;RUB;op-1;Оплата;Магазин\n"
	res, err := svc.Import(ctx, usecasestmt.ImportCommand{
		AccountID: "acc-uuid-1",
		FileName:  "/tmp/secret_path/november.csv",
		Reader:    strings.NewReader(csvData),
	})
	if err != nil {
		t.Fatalf("Import failed: %v", err)
	}
	if res.ImportedCount != 1 || res.ImportID != "imp-123" {
		t.Fatalf("unexpected ImportResult: %+v", res)
	}
	// 1. Проверяем, что в JSON-логах techlogger присутствуют request_id, user_id, job=statement_import и очищенный file_name.
	lines := strings.Split(strings.TrimSpace(logBuf.String()), "\n")
	if len(lines) < 2 {
		t.Fatalf("expected at least 2 structured log records, got %d", len(lines))
	}
	var lastRecord map[string]any
	if err := json.Unmarshal([]byte(lines[len(lines)-1]), &lastRecord); err != nil {
		t.Fatalf("invalid JSON log line: %v", err)
	}
	if lastRecord["request_id"] != "req-trace-99" || lastRecord["user_id"] != "user-uuid-7" || lastRecord["job"] != "statement_import" {
		t.Fatalf("missing correlation fields in techlogger output: %+v", lastRecord)
	}
	if lastRecord["file_name"] != "november.csv" {
		t.Fatalf("expected sanitized base file_name, got %v", lastRecord["file_name"])
	}
	// 2. Проверяем запись бизнес-аудита AuditActionStatementImported.
	events := auditRepo.ListByUser("user-uuid-7", 10)
	if len(events) != 1 {
		t.Fatalf("expected 1 audit event recorded, got %d", len(events))
	}
	ev := events
	if ev.Action != domain.AuditActionStatementImported || ev.Status != domain.AuditStatusSuccess {
		t.Fatalf("unexpected audit action/status: %+v", ev)
	}
	if ev.RequestID != "req-trace-99" || ev.ResourceID != "imp-123" || ev.Metadata["imported_count"] != "1" {
		t.Fatalf("unexpected audit event details: %+v", ev)
	}
}

var _ = io.EOF
var _ = time.UTC
