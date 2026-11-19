package store

import (
	"fmt"
	"time"

	"github.com/2inxs/openbanking-service/internal/mockbank/model"
)

var msk = time.FixedZone("MSK", 3*60*60)
var seedStart = time.Date(2026, 11, 1, 0, 0, 0, 0, msk)

func at(day, hour, minute int) time.Time {
	return seedStart.AddDate(0, 0, day-1).
		Add(time.Duration(hour)*time.Hour + time.Duration(minute)*time.Minute)
}

func minor(units, kop int64) int64 {
	return units*100 + kop
}

func SeedDemoData(s *Store) error {
	clients := []model.Client{
		{ID: "cl-001", FullName: "Анна Иванова"},
		{ID: "cl-002", FullName: "Борис Петров"},
	}

	accounts := []model.Account{
		{
			ID:          "acc-001",
			ClientID:    "cl-001",
			Number:      "40817810000000000001",
			Currency:    "RUB",
			SubType:     model.CurrentAccount,
			Status:      model.Enabled,
			Description: "Основной счёт",
			BalanceKop:  minor(50000, 0),
			Card: &model.Card{
				Scheme:       "VISA",
				MaskedNumber: "4276 **** **** 1234",
				HolderName:   "ANNA IVANOVA",
			},
			OpenedAt: time.Date(2023, 3, 15, 12, 0, 0, 0, msk),
		},
		{
			ID:          "acc-002",
			ClientID:    "cl-001",
			Number:      "40817810000000000002",
			Currency:    "RUB",
			SubType:     model.CreditCard,
			Status:      model.Enabled,
			Description: "Кредитная карта",
			BalanceKop:  0,
			Card: &model.Card{
				Scheme:       "MasterCard",
				MaskedNumber: "5469 **** **** 5678",
				HolderName:   "ANNA IVANOVA",
			},
			OpenedAt: time.Date(2024, 6, 1, 12, 0, 0, 0, msk),
		},
		{
			ID:          "acc-003",
			ClientID:    "cl-001",
			Number:      "42301840000000000003",
			Currency:    "USD",
			SubType:     model.Savings,
			Status:      model.Enabled,
			Description: "Накопительный в долларах",
			BalanceKop:  minor(1200, 0),
			OpenedAt:    time.Date(2025, 1, 20, 12, 0, 0, 0, msk),
		},
		{
			ID:          "acc-004",
			ClientID:    "cl-002",
			Number:      "40817810000000000004",
			Currency:    "RUB",
			SubType:     model.CurrentAccount,
			Status:      model.Enabled,
			Description: "Зарплатный счёт",
			BalanceKop:  minor(20000, 0),
			Card: &model.Card{
				Scheme:       "VISA",
				MaskedNumber: "4279 **** **** 9012",
				HolderName:   "BORIS PETROV",
			},
			OpenedAt: time.Date(2022, 9, 10, 12, 0, 0, 0, msk),
		},
		{
			ID:          "acc-005",
			ClientID:    "cl-002",
			Number:      "42301810000000000005",
			Currency:    "RUB",
			SubType:     model.Savings,
			Status:      model.Deleted,
			Description: "Вклад (закрыт)",
			BalanceKop:  minor(3000, 0),
			OpenedAt:    time.Date(2024, 2, 1, 12, 0, 0, 0, msk),
		},
	}
	transactions := []model.Transaction{
		// --- acc-001: Анна, основной счёт (RUB) ---
		{
			ID: "tx-0006", AccountID: "acc-001",
			Amount: minor(890, 0), Direction: model.Debit, Status: model.TransactionPending,
			BookedAt: at(18, 19, 40), ValueAt: at(18, 19, 40),
			Description: "Оплата в аптеке", MerchantName: "Аптека «Здоровье»", MCC: "5912", ByCard: true,
		},
		{
			ID: "tx-0001", AccountID: "acc-001",
			Amount: minor(80_000, 0), Direction: model.Credit, Status: model.TransactionBooked,
			BookedAt: at(5, 10, 0), ValueAt: at(5, 10, 0),
			Description: "Зарплата за октябрь", MerchantName: "", MCC: "", ByCard: false,
		},
		{
			ID: "tx-0002", AccountID: "acc-001",
			Amount: minor(3_456, 70), Direction: model.Debit, Status: model.TransactionBooked,
			BookedAt: at(6, 18, 25), ValueAt: at(6, 18, 25),
			Description: "Покупка продуктов", MerchantName: "Супермаркет «Колос»", MCC: "5411", ByCard: true,
		},
		// Две настоящие одинаковые покупки: одна сумма, магазин и время, но разные ID (требование #14).
		{
			ID: "tx-0004", AccountID: "acc-001",
			Amount: minor(350, 0), Direction: model.Debit, Status: model.TransactionBooked,
			BookedAt: at(7, 9, 15), ValueAt: at(7, 9, 15),
			Description: "Оплата в кафе", MerchantName: "Кофейня «Зерно»", MCC: "5814", ByCard: true,
		},
		{
			ID: "tx-0005", AccountID: "acc-001",
			Amount: minor(350, 0), Direction: model.Debit, Status: model.TransactionBooked,
			BookedAt: at(7, 9, 15), ValueAt: at(7, 9, 15),
			Description: "Оплата в кафе", MerchantName: "Кофейня «Зерно»", MCC: "5814", ByCard: true,
		},
		{
			ID: "tx-0003", AccountID: "acc-001",
			Amount: minor(612, 0), Direction: model.Debit, Status: model.TransactionBooked,
			BookedAt: at(6, 23, 10), ValueAt: at(6, 23, 10),
			Description: "Поездка на такси", MerchantName: "Такси «Быстро»", MCC: "4121", ByCard: true,
		},

		// --- acc-002: Анна, кредитная карта (RUB) ---
		{
			ID: "tx-0007", AccountID: "acc-002",
			Amount: minor(4_200, 0), Direction: model.Debit, Status: model.TransactionBooked,
			BookedAt: at(9, 20, 30), ValueAt: at(9, 20, 30),
			Description: "Ужин в ресторане", MerchantName: "Ресторан «Печь»", MCC: "5812", ByCard: true,
		},
		{
			ID: "tx-0008", AccountID: "acc-002",
			Amount: minor(12_990, 0), Direction: model.Debit, Status: model.TransactionBooked,
			BookedAt: at(12, 14, 5), ValueAt: at(12, 14, 5),
			Description: "Покупка электроники", MerchantName: "Магазин «Техно»", MCC: "5732", ByCard: true,
		},

		// --- acc-003: Анна, накопительный (USD) ---
		{
			ID: "tx-0010", AccountID: "acc-003",
			Amount: minor(2, 50), Direction: model.Credit, Status: model.TransactionBooked,
			BookedAt: at(15, 0, 5), ValueAt: at(15, 0, 5),
			Description: "Начисление процентов", MerchantName: "", MCC: "", ByCard: false,
		},
		{
			ID: "tx-0009", AccountID: "acc-003",
			Amount: minor(100, 0), Direction: model.Credit, Status: model.TransactionBooked,
			BookedAt: at(3, 11, 0), ValueAt: at(3, 11, 0),
			Description: "Пополнение счёта", MerchantName: "", MCC: "", ByCard: false,
		},

		// --- acc-004: Борис, зарплатный (RUB) ---
		{
			ID: "tx-0011", AccountID: "acc-004",
			Amount: minor(65_000, 0), Direction: model.Credit, Status: model.TransactionBooked,
			BookedAt: at(10, 9, 0), ValueAt: at(10, 9, 0),
			Description: "Зарплата за октябрь", MerchantName: "", MCC: "", ByCard: false,
		},
		{
			ID: "tx-0013", AccountID: "acc-004",
			Amount: minor(5_870, 40), Direction: model.Debit, Status: model.TransactionBooked,
			BookedAt: at(14, 12, 0), ValueAt: at(14, 12, 0),
			Description: "Оплата ЖКХ", MerchantName: "УК «Дом»", MCC: "4900", ByCard: false,
		},
		{
			ID: "tx-0012", AccountID: "acc-004",
			Amount: minor(2_134, 0), Direction: model.Debit, Status: model.TransactionBooked,
			BookedAt: at(11, 19, 45), ValueAt: at(11, 19, 45),
			Description: "Покупка продуктов", MerchantName: "Супермаркет «Колос»", MCC: "5411", ByCard: true,
		},
		// --- acc-005: Борис, закрытый вклад (RUB) ---
		{
			ID: "tx-0014", AccountID: "acc-005",
			Amount: minor(3_000, 0), Direction: model.Debit, Status: model.TransactionBooked,
			BookedAt: at(2, 10, 0), ValueAt: at(2, 10, 0),
			Description: "Закрытие вклада, перевод на счёт", MerchantName: "", MCC: "", ByCard: false,
		},
	}

	for _, c := range clients {
		if err := s.AddClient(c); err != nil {
			return fmt.Errorf("генерация клиентов %s: %w", c.ID, err)
		}
	}
	for _, acc := range accounts {
		if err := s.AddAccount(acc); err != nil {
			return fmt.Errorf("генерация счетов %s: %w", acc.ID, err)
		}
	}
	for _, trx := range transactions {
		if err := s.AddTransaction(trx); err != nil {
			return fmt.Errorf("генерация платежей %s: %w", trx.ID, err)
		}
	}
	return nil
}
