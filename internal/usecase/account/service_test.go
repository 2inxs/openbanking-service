package account_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/2inxs/openbanking-service/internal/domain"
	"github.com/2inxs/openbanking-service/internal/domain/money"
	"github.com/2inxs/openbanking-service/internal/usecase/account"
	"github.com/2inxs/openbanking-service/pkg/logger"
)

type stubAccountRepo struct {
	accounts map[domain.AccountID]domain.Account
	err      error
	calls    int
	// bypassOwnershipFilter имитирует репозиторий, который не отфильтровал userID,
	// чтобы проверить защиту на уровне usecase.
	bypassOwnershipFilter bool
}

func (r *stubAccountRepo) Get(_ context.Context, userID domain.UserID, id domain.AccountID) (domain.Account, error) {
	r.calls++
	if r.err != nil {
		return domain.Account{}, r.err
	}
	acc, ok := r.accounts[id]
	if !ok {
		return domain.Account{}, domain.ErrAccountNotFound
	}
	if !r.bypassOwnershipFilter && acc.UserID != userID {
		return domain.Account{}, domain.ErrAccountNotFound
	}
	return acc, nil
}

func (r *stubAccountRepo) ListByUser(_ context.Context, userID domain.UserID) ([]domain.Account, error) {
	r.calls++
	if r.err != nil {
		return nil, r.err
	}
	var out []domain.Account
	for _, acc := range r.accounts {
		if r.bypassOwnershipFilter || acc.UserID == userID {
			out = append(out, acc)
		}
	}
	return out, nil
}

func mustAmount(t *testing.T, raw string) money.Amount {
	t.Helper()
	a, err := money.Parse(raw)
	if err != nil {
		t.Fatal(err)
	}
	return a
}

func TestAccountServiceListAndGet(t *testing.T) {
	ctx := context.Background()
	asOf := time.Date(2026, 11, 25, 12, 30, 0, 0, time.UTC)

	user1 := domain.UserID("user-1")
	user2 := domain.UserID("user-2")
	acc1ID := domain.AccountID("acc-1")
	acc2ID := domain.AccountID("acc-2")
	accForeignID := domain.AccountID("acc-foreign")

	repo := &stubAccountRepo{
		accounts: map[domain.AccountID]domain.Account{
			acc1ID: {
				ID:       acc1ID,
				UserID:   user1,
				BankID:   "bank-1",
				Number:   "40817810000000000001",
				Name:     "Основной рублёвый",
				Type:     domain.AccountCurrent,
				Status:   domain.AccountActive,
				Currency: money.RUB,
				Balance: &domain.BalanceSnapshot{
					Value: money.Money{Amount: mustAmount(t, "15240.50"), Currency: money.RUB},
					AsOf:  asOf,
				},
			},
			acc2ID: {
				ID:       acc2ID,
				UserID:   user1,
				BankID:   "bank-1",
				Number:   "40817840000000000002",
				Name:     "Валютный без остатка",
				Type:     domain.AccountSavings,
				Status:   domain.AccountActive,
				Currency: "USD",
				Balance:  nil, // остаток неизвестен
			},
			accForeignID: {
				ID:       accForeignID,
				UserID:   user2,
				BankID:   "bank-1",
				Number:   "40817810000000009999",
				Name:     "Чужой счёт",
				Type:     domain.AccountCurrent,
				Status:   domain.AccountActive,
				Currency: money.RUB,
			},
		},
	}

	svc := account.NewService(repo)

	// 1. Список счетов user-1: 2 счёта, у первого есть остаток и AsOf, у второго Balance == nil.
	list, err := svc.ListAccounts(ctx, user1)
	if err != nil {
		t.Fatalf("ListAccounts(user1) failed: %v", err)
	}
	if len(list) != 2 {
		t.Fatalf("expected 2 accounts for user1, got %d", len(list))
	}

	byID := make(map[domain.AccountID]account.AccountSummary, len(list))
	for _, item := range list {
		byID[item.ID] = item
	}
	if byID[acc1ID].Balance == nil || byID[acc1ID].Balance.Amount.String() != "15240.5000" || !byID[acc1ID].Balance.AsOf.Equal(asOf) {
		t.Fatalf("unexpected acc1 balance: %+v", byID[acc1ID].Balance)
	}
	if byID[acc2ID].Balance != nil || byID[acc2ID].Currency != "USD" {
		t.Fatalf("expected nil balance and USD currency for acc2, got %+v", byID[acc2ID])
	}

	// 2. Пользователь без счетов получает пустой ненулевой срез.
	emptyList, err := svc.ListAccounts(ctx, "user-empty")
	if err != nil {
		t.Fatalf("ListAccounts(user-empty) failed: %v", err)
	}
	if emptyList == nil || len(emptyList) != 0 {
		t.Fatalf("expected non-nil empty slice, got %#v", emptyList)
	}

	// 3. Получение конкретного счёта владельцем.
	got1, err := svc.GetAccount(ctx, user1, acc1ID)
	if err != nil {
		t.Fatalf("GetAccount(user1, acc1) failed: %v", err)
	}
	if got1.ID != acc1ID || got1.Currency != money.RUB || got1.Balance == nil || got1.Balance.Amount.String() != "15240.5000" {
		t.Fatalf("unexpected GetAccount result: %+v", got1)
	}

	// 4. Несуществующий счёт -> ErrAccountNotFound.
	if _, err := svc.GetAccount(ctx, user1, "non-existent"); !errors.Is(err, account.ErrAccountNotFound) {
		t.Fatalf("expected ErrAccountNotFound for unknown account, got %v", err)
	}

	// 5. Чужой счёт -> ErrAccountNotFound (в том числе если репозиторий не отфильтровал userID).
	repo.bypassOwnershipFilter = true
	if _, err := svc.GetAccount(ctx, user1, accForeignID); !errors.Is(err, account.ErrAccountNotFound) {
		t.Fatalf("expected ErrAccountNotFound for foreign account, got %v", err)
	}
	repo.bypassOwnershipFilter = false
}

func TestAccountServiceValidationAndErrors(t *testing.T) {
	ctx := context.Background()
	repo := &stubAccountRepo{}
	svc := account.NewService(repo)

	if _, err := svc.ListAccounts(ctx, ""); !errors.Is(err, account.ErrInvalidInput) {
		t.Fatalf("expected ErrInvalidInput for empty userID, got %v", err)
	}
	if _, err := svc.ListAccounts(ctx, " user-1 "); !errors.Is(err, account.ErrInvalidInput) {
		t.Fatalf("expected ErrInvalidInput for padded userID, got %v", err)
	}
	if _, err := svc.GetAccount(ctx, "", "acc-1"); !errors.Is(err, account.ErrInvalidInput) {
		t.Fatalf("expected ErrInvalidInput for empty userID in GetAccount, got %v", err)
	}
	if _, err := svc.GetAccount(ctx, "user-1", ""); !errors.Is(err, account.ErrInvalidInput) {
		t.Fatalf("expected ErrInvalidInput for empty accountID in GetAccount, got %v", err)
	}
	if repo.calls != 0 {
		t.Fatalf("expected repository not to be called on invalid inputs, calls=%d", repo.calls)
	}

	dbErr := errors.New("db connection reset")
	repo.err = dbErr
	if _, err := svc.ListAccounts(ctx, "user-1"); !errors.Is(err, dbErr) {
		t.Fatalf("expected propagated repo error, got %v", err)
	}
}

func TestAccountServiceReconciliationAndSecurityBoundaries(t *testing.T) {
	ctx := context.Background()
	asOf := time.Date(2026, 11, 25, 14, 0, 0, 0, time.UTC)

	// 1. Сверка валют: если репозиторий вернул счёт RUB с остатком в USD — возвращается ErrCurrencyMismatch.
	badRepo := &stubAccountRepo{
		accounts: map[domain.AccountID]domain.Account{
			"acc-mismatch": {
				ID:       "acc-mismatch",
				UserID:   "user-1",
				BankID:   "bank-1",
				Number:   "40817810000000000001",
				Name:     "Счёт с битой валютой остатка",
				Type:     domain.AccountCurrent,
				Status:   domain.AccountActive,
				Currency: money.RUB,
				Balance: &domain.BalanceSnapshot{
					Value: money.Money{Amount: mustAmount(t, "100.00"), Currency: "USD"},
					AsOf:  asOf,
				},
			},
		},
	}
	svc := account.NewService(badRepo)

	if _, err := svc.GetAccount(ctx, "user-1", "acc-mismatch"); !errors.Is(err, money.ErrCurrencyMismatch) {
		t.Fatalf("expected ErrCurrencyMismatch on balance currency mismatch, got %v", err)
	}
	if _, err := svc.ListAccounts(ctx, "user-1"); !errors.Is(err, money.ErrCurrencyMismatch) {
		t.Fatalf("expected ErrCurrencyMismatch in ListAccounts, got %v", err)
	}

	// 2. Безопасность: несовпадение userID с контекстом авторизованного пользователя отклоняется до обращения к БД.
	badRepo.calls = 0
	authCtx := logger.WithFields(ctx, func(f *logger.ContextFields) {
		f.UserID = "authenticated-user"
	})
	if _, err := svc.GetAccount(authCtx, "user-1", "acc-mismatch"); !errors.Is(err, account.ErrAccountNotFound) {
		t.Fatalf("expected ErrAccountNotFound when userID mismatches context user, got %v", err)
	}
	if _, err := svc.ListAccounts(authCtx, "user-1"); !errors.Is(err, account.ErrAccountNotFound) {
		t.Fatalf("expected ErrAccountNotFound in ListAccounts when userID mismatches context user, got %v", err)
	}
	if badRepo.calls != 0 {
		t.Fatalf("expected repo not to be called on context user mismatch, calls=%d", badRepo.calls)
	}

	// 3. Граничные проверки ID: управляющие символы и длина > 128 отклоняются с ErrInvalidInput.
	if _, err := svc.GetAccount(ctx, "user\n1", "acc-1"); !errors.Is(err, account.ErrInvalidInput) {
		t.Fatalf("expected ErrInvalidInput for newline in userID, got %v", err)
	}
	if _, err := svc.ListAccounts(ctx, domain.UserID(strings.Repeat("a", 129))); !errors.Is(err, account.ErrInvalidInput) {
		t.Fatalf("expected ErrInvalidInput for oversized userID, got %v", err)
	}
}
