-- Выполняется после up. Все тестовые изменения откатываются.
BEGIN;

CREATE FUNCTION pg_temp.expect_rejection(label text, statement text, expected_state text)
RETURNS void LANGUAGE plpgsql AS $$
BEGIN
  BEGIN
    EXECUTE statement;
  EXCEPTION WHEN OTHERS THEN
    IF SQLSTATE = ANY(string_to_array(expected_state, ',')) THEN RETURN; END IF;
    RAISE EXCEPTION '%: expected SQLSTATE %, got % (%)', label, expected_state, SQLSTATE, SQLERRM;
  END;
  RAISE EXCEPTION '%: invalid change was accepted', label;
END;
$$;

DO $test$
DECLARE
  owner_id uuid := '10000000-0000-4000-8000-000000000001';
  other_owner_id uuid := '10000000-0000-4000-8000-000000000002';
  bank uuid := '00000000-0000-4000-8000-000000000001';
  connection_id uuid := '20000000-0000-4000-8000-000000000001';
  other_connection_id uuid := '20000000-0000-4000-8000-000000000002';
  account uuid := '30000000-0000-4000-8000-000000000001';
  other_account uuid := '30000000-0000-4000-8000-000000000002';
  consent uuid := '40000000-0000-4000-8000-000000000001';
  other_consent uuid := '40000000-0000-4000-8000-000000000002';
  tx uuid := '50000000-0000-4000-8000-000000000001';
  test_import uuid := '60000000-0000-4000-8000-000000000001';
  value_version bigint;
  value_status text;
  count_rows integer;
BEGIN
  IF NOT EXISTS (
    SELECT 1 FROM users WHERE id='00000000-0000-4000-8000-000000000100'
      AND email='demo@openbanking.invalid' AND password_hash='!'
  ) THEN RAISE EXCEPTION 'Demo user is missing or has an enabled shared password'; END IF;
  SELECT count(*) INTO count_rows FROM currencies WHERE code IN ('RUB','USD','EUR','JPY');
  IF count_rows <> 4 THEN RAISE EXCEPTION 'Currency seed is incomplete'; END IF;
  SELECT count(*) INTO count_rows FROM transaction_categories WHERE id IN (1,2,3,4);
  IF count_rows <> 4 THEN RAISE EXCEPTION 'Category seed is incomplete'; END IF;

  INSERT INTO users(id,email,password_hash) VALUES
    (owner_id,'MigrationTest@example.invalid','!'),
    (other_owner_id,'OtherMigrationTest@example.invalid','!');
  INSERT INTO bank_connections(id,user_id,bank_id) VALUES
    (connection_id,owner_id,bank),(other_connection_id,other_owner_id,bank);
  INSERT INTO accounts(id,user_id,bank_id,bank_connection_id,external_account_id,name,currency_code) VALUES
    (account,owner_id,bank,connection_id,'migration-acc-1','Первый','RUB'),
    (other_account,other_owner_id,bank,other_connection_id,'migration-acc-2','Второй','RUB');
  INSERT INTO bank_consents(id,bank_connection_id,bank_id,external_consent_id,status,bank_created_at,status_updated_at) VALUES
    (consent,connection_id,bank,repeat('x',100),'authorised',now(),now()),
    (other_consent,other_connection_id,bank,'migration-consent-2','authorised',now(),now());
  INSERT INTO bank_credentials(bank_consent_id,access_token_encrypted,encryption_key_id)
    VALUES (consent,decode('ab','hex'),'test-key');

  PERFORM pg_temp.expect_rejection('email ignores case',
    $$INSERT INTO users(email,password_hash) VALUES ('migrationtest@example.invalid','!')$$,'23505');
  PERFORM pg_temp.expect_rejection('connection ownership',format(
    'INSERT INTO accounts(user_id,bank_id,bank_connection_id,external_account_id,name,currency_code) VALUES (%L,%L,%L,%L,%L,%L)',
    other_owner_id,bank,connection_id,'wrong-owner','Wrong','RUB'),'23503');
  PERFORM pg_temp.expect_rejection('external account uniqueness',format(
    'INSERT INTO accounts(user_id,bank_id,bank_connection_id,external_account_id,name,currency_code) VALUES (%L,%L,%L,%L,%L,%L)',
    other_owner_id,bank,other_connection_id,'migration-acc-1','Duplicate','RUB'),'23505');
  PERFORM pg_temp.expect_rejection('invalid connection status',format(
    'UPDATE bank_connections SET status=%L WHERE id=%L','unknown-status',connection_id),'23514');
  PERFORM pg_temp.expect_rejection('negative retry counter',format(
    'UPDATE bank_connections SET failed_attempts=-1 WHERE id=%L',connection_id),'23514');
  PERFORM pg_temp.expect_rejection('one authorised consent',format(
    'INSERT INTO bank_consents(bank_connection_id,bank_id,external_consent_id,status,bank_created_at,status_updated_at) VALUES (%L,%L,%L,%L,now(),now())',
    connection_id,bank,'duplicate-active','authorised'),'23505');
  PERFORM pg_temp.expect_rejection('external consent uniqueness',format(
    'INSERT INTO bank_consents(bank_connection_id,bank_id,external_consent_id,bank_created_at,status_updated_at) VALUES (%L,%L,%L,now(),now())',
    connection_id,bank,'migration-consent-2'),'23505');
  PERFORM pg_temp.expect_rejection('consent period',format(
    'UPDATE bank_consents SET transactions_from=%L,transactions_to=%L WHERE id=%L',
    '2026-11-23','2026-11-22',consent),'23514');
  PERFORM pg_temp.expect_rejection('credentials one to one',format(
    'INSERT INTO bank_credentials(bank_consent_id,access_token_encrypted,encryption_key_id) VALUES (%L,decode(%L,%L),%L)',
    consent,'ab','hex','test-key'),'23505');

  UPDATE bank_connections SET status='active' WHERE id=connection_id AND version=0;
  SELECT version INTO value_version FROM bank_connections WHERE id=connection_id;
  IF value_version <> 1 THEN RAISE EXCEPTION 'Connection version did not advance'; END IF;
  UPDATE bank_connections SET status='error' WHERE id=connection_id AND version=0;
  SELECT status INTO value_status FROM bank_connections WHERE id=connection_id;
  IF value_status <> 'active' THEN RAISE EXCEPTION 'Stale connection update was accepted'; END IF;
  UPDATE bank_credentials SET access_token_encrypted=decode('cd','hex') WHERE bank_consent_id=consent AND version=0;
  SELECT version INTO value_version FROM bank_credentials WHERE bank_consent_id=consent;
  IF value_version <> 1 THEN RAISE EXCEPTION 'Credential version did not advance'; END IF;

  INSERT INTO bank_oauth_attempts(bank_connection_id,state_hash,nonce_hash,pkce_verifier_encrypted,encryption_key_id,expires_at)
    VALUES(connection_id,repeat('a',64),repeat('b',64),decode('ab','hex'),'test-key',now()+interval '10 minutes');
  PERFORM pg_temp.expect_rejection('nonce uniqueness',format(
    'INSERT INTO bank_oauth_attempts(bank_connection_id,state_hash,nonce_hash,pkce_verifier_encrypted,encryption_key_id,expires_at) VALUES (%L,%L,%L,decode(%L,%L),%L,now()+interval %L)',
    connection_id,repeat('c',64),repeat('b',64),'ab','hex','test-key','10 minutes'),'23505');
  PERFORM pg_temp.expect_rejection('OAuth consent belongs to connection',format(
    'INSERT INTO bank_oauth_attempts(bank_connection_id,bank_consent_id,state_hash,pkce_verifier_encrypted,encryption_key_id,expires_at) VALUES (%L,%L,%L,decode(%L,%L),%L,now()+interval %L)',
    connection_id,other_consent,repeat('d',64),'ab','hex','test-key','10 minutes'),'23514');

  INSERT INTO statement_imports(account_id,file_name,file_sha256,status)
    VALUES(account,'a.csv',repeat('a',64),'failed');
  INSERT INTO statement_imports(id,account_id,file_name,file_sha256,status)
    VALUES(test_import,account,'a.csv',repeat('a',64),'processing');
  PERFORM pg_temp.expect_rejection('duplicate nonfailed file',format(
    'INSERT INTO statement_imports(account_id,file_name,file_sha256) VALUES (%L,%L,%L)',
    account,'a.csv',repeat('a',64)),'23505');
  UPDATE statement_imports SET status='completed' WHERE id=test_import;

  INSERT INTO transactions(id,account_id,external_transaction_id,source,status,amount,currency_code,occurred_at,booked_at,deduplication_key)
    VALUES(tx,account,'migration-tx-1','open_banking','booked',-10,'RUB','2026-11-20','2026-11-20',repeat('1',64));
  -- Одинаковый ID разрешён на другом счёте.
  INSERT INTO transactions(account_id,external_transaction_id,source,status,amount,currency_code,occurred_at,booked_at,deduplication_key)
    VALUES(other_account,'migration-tx-1','open_banking','booked',-10,'RUB','2026-11-20','2026-11-20',repeat('1',64));
  -- Разные платежи с одинаковыми суммой и временем не склеиваются.
  INSERT INTO transactions(account_id,source,status,amount,currency_code,occurred_at,booked_at,deduplication_key)
    VALUES(account,'statement','booked',-10,'RUB','2026-11-20','2026-11-20',repeat('2',64));
  PERFORM pg_temp.expect_rejection('external transaction uniqueness',format(
    'INSERT INTO transactions(account_id,external_transaction_id,source,status,amount,currency_code,occurred_at,booked_at,deduplication_key) VALUES (%L,%L,%L,%L,-10,%L,now(),now(),%L)',
    account,'migration-tx-1','open_banking','booked','RUB',repeat('3',64)),'23505');
  PERFORM pg_temp.expect_rejection('dedup uniqueness',format(
    'INSERT INTO transactions(account_id,source,status,amount,currency_code,occurred_at,booked_at,deduplication_key) VALUES (%L,%L,%L,-10,%L,now(),now(),%L)',
    account,'statement','booked','RUB',repeat('1',64)),'23505');
  PERFORM pg_temp.expect_rejection('transaction currency matches account',format(
    'UPDATE transactions SET currency_code=%L WHERE id=%L','USD',tx),'23503');
  PERFORM pg_temp.expect_rejection('import belongs to account',format(
    'UPDATE transactions SET statement_import_id=%L WHERE account_id=%L',test_import,other_account),'23503');
  PERFORM pg_temp.expect_rejection('external transaction ID immutable',format(
    'UPDATE transactions SET external_transaction_id=%L WHERE id=%L','replacement',tx),'23514');
  PERFORM pg_temp.expect_rejection('external transaction ID cannot be cleared',format(
    'UPDATE transactions SET external_transaction_id=NULL WHERE id=%L',tx),'23514');
  PERFORM pg_temp.expect_rejection('transaction cannot move accounts',format(
    'UPDATE transactions SET account_id=%L WHERE id=%L',other_account,tx),'23514');
  PERFORM pg_temp.expect_rejection('feed time stable',format(
    'UPDATE transactions SET occurred_at=occurred_at+interval %L WHERE id=%L','1 day',tx),'23514');
  PERFORM pg_temp.expect_rejection('transaction needs bank date',format(
    'UPDATE transactions SET status=%L,booked_at=NULL,value_at=NULL WHERE id=%L','pending',tx),'23514');
  PERFORM pg_temp.expect_rejection('booked needs booked_at',format(
    'UPDATE transactions SET booked_at=NULL,value_at=now() WHERE id=%L',tx),'23514');
  PERFORM pg_temp.expect_rejection('original money must be paired',format(
    'UPDATE transactions SET original_amount=-1 WHERE id=%L',tx),'23514');
  PERFORM pg_temp.expect_rejection('original amount direction',format(
    'UPDATE transactions SET original_amount=1,original_currency_code=%L WHERE id=%L','USD',tx),'23514');
  PERFORM pg_temp.expect_rejection('MCC has four digits',format(
    'UPDATE transactions SET mcc=%L WHERE id=%L','ABCD',tx),'23514');
  PERFORM pg_temp.expect_rejection('balance needs timestamp',format(
    'UPDATE accounts SET reported_balance=10 WHERE id=%L',account),'23514');
  PERFORM pg_temp.expect_rejection('history blocks account deletion',format(
    'DELETE FROM accounts WHERE id=%L',account),'23503,23001');
  PERFORM pg_temp.expect_rejection('positive rate',
    $$INSERT INTO exchange_rates(currency_code,rate_date,rub_per_unit) VALUES ('USD','2026-11-20',0)$$,'23514');
  PERFORM pg_temp.expect_rejection('finite rate',
    $$INSERT INTO exchange_rates(currency_code,rate_date,rub_per_unit) VALUES ('USD','2026-11-20','NaN')$$,'23514');
  UPDATE transactions SET description='updated' WHERE id=tx AND row_version=0;
  SELECT row_version INTO value_version FROM transactions WHERE id=tx;
  IF value_version <> 1 THEN RAISE EXCEPTION 'Transaction version did not advance'; END IF;

  INSERT INTO audit_logs(service,event_code,outcome,message,user_id,details)
    VALUES('api','migration_test','success','ok','99999999-0000-4000-8000-000000000001','{"added":true}');
  PERFORM pg_temp.expect_rejection('audit details are an object',
    $$INSERT INTO audit_logs(service,event_code,outcome,message,details) VALUES ('api','test','success','ok','[]')$$,'23514');
  RAISE NOTICE 'Migration integrity checks passed';
END;
$test$;

ROLLBACK;
