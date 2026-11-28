package statement_test

import (
	"errors"
	"strings"
	"testing"

	"github.com/2inxs/openbanking-service/internal/domain"
	st "github.com/2inxs/openbanking-service/internal/usecase/statement"
)

func TestImportStatusTransitions(t *testing.T) {
	statuses := []st.ImportStatus{st.ImportPending, st.ImportProcessing, st.ImportCompleted, st.ImportFailed, "unknown"}
	allowed := map[[2]st.ImportStatus]bool{
		{st.ImportPending, st.ImportProcessing}:   true,
		{st.ImportPending, st.ImportFailed}:       true,
		{st.ImportProcessing, st.ImportCompleted}: true,
		{st.ImportProcessing, st.ImportFailed}:    true,
	}
	for _, from := range statuses {
		for _, to := range statuses {
			t.Run(string(from)+"/"+string(to), func(t *testing.T) {
				if got := st.CanTransition(from, to); got != allowed[[2]st.ImportStatus{from, to}] {
					t.Fatalf("transition %s -> %s: allowed=%v", from, to, got)
				}
			})
		}
	}
}

func TestImportRecordValidation(t *testing.T) {
	base := st.ImportRecord{FileName: "statement.csv", FileSHA256: strings.Repeat("a", 64), Status: st.ImportPending}
	tests := []struct {
		name      string
		change    func(*st.ImportRecord)
		valid     bool
		creatable bool
	}{
		{"pending", func(*st.ImportRecord) {}, true, true},
		{"processing", func(i *st.ImportRecord) { i.Status = st.ImportProcessing }, true, false},
		{"completed", func(i *st.ImportRecord) { i.Status = st.ImportCompleted; i.ImportedCount = 5; i.SkippedCount = 2 }, true, false},
		{"failed", func(i *st.ImportRecord) {
			i.Status = st.ImportFailed
			i.ErrorCount = 1
			i.ErrorMessage = "validation_failed"
		}, true, true},
		{"failed_with_skips", func(i *st.ImportRecord) { i.Status = st.ImportFailed; i.SkippedCount = 1 }, true, false},
		{"unknown_status", func(i *st.ImportRecord) { i.Status = "unknown" }, false, false},
		{"completed_with_errors", func(i *st.ImportRecord) { i.Status = st.ImportCompleted; i.ErrorCount = 1 }, false, false},
		{"completed_with_message", func(i *st.ImportRecord) { i.Status = st.ImportCompleted; i.ErrorMessage = "failed" }, false, false},
		{"failed_with_imports", func(i *st.ImportRecord) { i.Status = st.ImportFailed; i.ImportedCount = 1 }, false, false},
		{"pending_with_imports", func(i *st.ImportRecord) { i.ImportedCount = 1 }, false, false},
		{"pending_with_skips", func(i *st.ImportRecord) { i.SkippedCount = 1 }, false, false},
		{"pending_with_errors", func(i *st.ImportRecord) { i.ErrorCount = 1 }, false, false},
		{"processing_with_message", func(i *st.ImportRecord) { i.Status = st.ImportProcessing; i.ErrorMessage = "failed" }, false, false},
		{"negative_imports", func(i *st.ImportRecord) { i.ImportedCount = -1 }, false, false},
		{"negative_skips", func(i *st.ImportRecord) { i.SkippedCount = -1 }, false, false},
		{"negative_errors", func(i *st.ImportRecord) { i.ErrorCount = -1 }, false, false},
		{"blank_filename", func(i *st.ImportRecord) { i.FileName = " " }, false, false},
		{"bad_hash", func(i *st.ImportRecord) { i.FileSHA256 = "bad" }, false, false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			i := base
			tc.change(&i)
			for _, check := range []struct {
				name  string
				err   error
				valid bool
			}{
				{"validate", i.Validate(), tc.valid},
				{"create", i.ValidateForCreate(), tc.creatable},
			} {
				if (check.err == nil) != check.valid || (check.err != nil && !errors.Is(check.err, domain.ErrInvalidModel)) {
					t.Fatalf("%s: valid=%v err=%v", check.name, check.valid, check.err)
				}
			}
		})
	}
}
