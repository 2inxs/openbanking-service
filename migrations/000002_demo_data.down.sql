BEGIN;

-- Без CASCADE: откат не должен удалять связанные пользовательские данные.
DELETE FROM users WHERE id = '00000000-0000-4000-8000-000000000100';
DELETE FROM banks WHERE id = '00000000-0000-4000-8000-000000000001';
DELETE FROM transaction_categories WHERE id IN (1, 2, 3, 4);
DELETE FROM currencies WHERE code IN ('RUB', 'USD', 'EUR', 'JPY');

COMMIT;
