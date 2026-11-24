package statement_test

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"

	"github.com/2inxs/openbanking-service/internal/domain/money"
	"github.com/2inxs/openbanking-service/internal/infrastructure/statement"
	usecasestmt "github.com/2inxs/openbanking-service/internal/usecase/statement"
)

func TestCSVParserParseStreamTwoGoroutinesChunks(t *testing.T) {
	const totalRecords = 2505 // 3 чанка: 1000 + 1000 + 505

	var sb strings.Builder
	sb.WriteString("account_number;booked_at;amount;currency;external_id;description;counterparty\n")
	for i := 1; i <= totalRecords; i++ {
		fmt.Fprintf(&sb, "40817810000000000001;2026-11-20;-10.50;RUB;op-%d;Покупка %d;Магазин\n", i, i)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	chunks := make(chan usecasestmt.TransactionChunk, 4)
	parser := statement.NewCSVParser()

	var (
		wg           sync.WaitGroup
		prodRowErrs  []usecasestmt.RowError
		prodErr      error
		receivedCh   int
		receivedRows int
		lastAccount  string
		lastCurrency money.Currency
	)

	// Горутина 1: потоковый парсер (Producer)
	wg.Add(1)
	go func() {
		defer wg.Done()
		prodRowErrs, prodErr = parser.ParseStream(ctx, strings.NewReader(sb.String()), chunks)
	}()

	// Горутина 2: потребитель чанков (Consumer)
	for chunk := range chunks {
		receivedCh++
		receivedRows += len(chunk.Rows)
		lastAccount = chunk.AccountNumber
		lastCurrency = chunk.Currency
	}

	wg.Wait()

	if prodErr != nil || len(prodRowErrs) != 0 {
		t.Fatalf("ParseStream unexpected error: err=%v rowErrs=%+v", prodErr, prodRowErrs)
	}
	if receivedCh != 3 {
		t.Fatalf("expected 3 chunks (1000+1000+505), got %d", receivedCh)
	}
	if receivedRows != totalRecords {
		t.Fatalf("expected %d rows, got %d", totalRecords, receivedRows)
	}
	if lastAccount != "40817810000000000001" || lastCurrency != money.RUB {
		t.Fatalf("unexpected account/currency: %s / %s", lastAccount, lastCurrency)
	}
}

func TestCSVParserParseStreamCancelsCleanlyOnConsumerError(t *testing.T) {
	var sb strings.Builder
	sb.WriteString("account_number;booked_at;amount;currency;external_id;description;counterparty\n")
	for i := 1; i <= 5000; i++ {
		fmt.Fprintf(&sb, "40817810000000000001;2026-11-20;-10.50;RUB;op-%d;Покупка;Магазин\n", i)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	chunks := make(chan usecasestmt.TransactionChunk)
	parser := statement.NewCSVParser()

	done := make(chan error, 1)
	go func() {
		_, err := parser.ParseStream(ctx, strings.NewReader(sb.String()), chunks)
		done <- err
	}()

	// Consumer читает первый чанк и имитирует ошибку БД (отменяет контекст)
	<-chunks
	cancel()

	err := <-done
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("expected context.Canceled after consumer abort, got %v", err)
	}
}
