BEGIN;

-- Только для отката в отдельной локальной/тестовой БД. История будет удалена.
DROP TABLE audit_logs;
DROP TABLE exchange_rates;
DROP TABLE transactions;
DROP TABLE statement_imports;
DROP TABLE accounts;
DROP TABLE bank_oauth_attempts;
DROP TABLE bank_credentials;
DROP TABLE bank_consents;
DROP TABLE bank_connections;
DROP TABLE users;
DROP TABLE banks;
DROP TABLE transaction_categories;
DROP TABLE currencies;

DROP FUNCTION check_oauth_consent_connection();
DROP FUNCTION guard_credentials_identity();
DROP FUNCTION guard_bank_consent_identity();
DROP FUNCTION guard_bank_connection_identity();
DROP FUNCTION guard_account_identity();
DROP FUNCTION guard_transaction_identity();
DROP FUNCTION touch_updated_at();

COMMIT;
