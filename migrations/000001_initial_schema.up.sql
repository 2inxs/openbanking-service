-- Схема v2, согласованная 2026-11-22. PostgreSQL 15+.
BEGIN;

CREATE TABLE "currencies" (
  "code" varchar(3) PRIMARY KEY,
  "name" varchar(100) NOT NULL,
  CONSTRAINT "currencies_code_format" CHECK (code ~ '^[A-Z]{3}$')
);

CREATE TABLE "transaction_categories" (
  "id" smallint PRIMARY KEY,
  "name" varchar(100) UNIQUE NOT NULL,
  "description" text
);

CREATE TABLE "banks" (
  "id" uuid PRIMARY KEY DEFAULT (gen_random_uuid()),
  "code" varchar(50) UNIQUE NOT NULL,
  "name" varchar(200) NOT NULL,
  "bic" varchar(9),
  "created_at" timestamptz NOT NULL DEFAULT (now()),
  "updated_at" timestamptz NOT NULL DEFAULT (now()),
  CONSTRAINT "banks_bic_format" CHECK (bic IS NULL OR bic ~ '^[0-9]{9}$')
);

CREATE TABLE "users" (
  "id" uuid PRIMARY KEY DEFAULT (gen_random_uuid()),
  "email" varchar(254) NOT NULL,
  "password_hash" text NOT NULL,
  "display_name" varchar(100),
  "created_at" timestamptz NOT NULL DEFAULT (now()),
  "updated_at" timestamptz NOT NULL DEFAULT (now()),
  CONSTRAINT "users_email_nonempty" CHECK (length(btrim(email)) > 0 AND email = btrim(email)),
  CONSTRAINT "users_password_hash_nonempty" CHECK (length(btrim(password_hash)) > 0)
);

CREATE TABLE "bank_connections" (
  "id" uuid PRIMARY KEY DEFAULT (gen_random_uuid()),
  "user_id" uuid NOT NULL,
  "bank_id" uuid NOT NULL,
  "external_customer_id" varchar(255),
  "status" text NOT NULL DEFAULT 'pending',
  "last_synced_at" timestamptz,
  "next_sync_at" timestamptz,
  "last_sync_error" text,
  "failed_attempts" integer NOT NULL DEFAULT 0,
  "last_manual_sync_at" timestamptz,
  "version" bigint NOT NULL DEFAULT 0,
  "created_at" timestamptz NOT NULL DEFAULT (now()),
  "updated_at" timestamptz NOT NULL DEFAULT (now()),
  CONSTRAINT "bank_connections_status_allowed" CHECK (status IN ('active', 'error', 'disconnected', 'pending')),
  CONSTRAINT "bank_connections_counters_nonnegative" CHECK (failed_attempts >= 0 AND version >= 0),
  CONSTRAINT "bank_connections_customer_nonempty" CHECK (external_customer_id IS NULL OR length(btrim(external_customer_id)) > 0)
);

CREATE TABLE "bank_consents" (
  "id" uuid PRIMARY KEY DEFAULT (gen_random_uuid()),
  "bank_connection_id" uuid NOT NULL,
  "bank_id" uuid NOT NULL,
  "external_consent_id" varchar(255) NOT NULL,
  "status" text NOT NULL DEFAULT 'awaiting_authorisation',
  "permissions" text[] NOT NULL DEFAULT ('{}'::text[]),
  "bank_created_at" timestamptz NOT NULL,
  "status_updated_at" timestamptz NOT NULL,
  "expires_at" timestamptz,
  "transactions_from" timestamptz,
  "transactions_to" timestamptz,
  "created_at" timestamptz NOT NULL DEFAULT (now()),
  "updated_at" timestamptz NOT NULL DEFAULT (now()),
  CONSTRAINT "bank_consents_status_allowed" CHECK (status IN ('awaiting_authorisation', 'authorised', 'rejected', 'revoked', 'expired')),
  CONSTRAINT "bank_consents_external_nonempty" CHECK (length(btrim(external_consent_id)) > 0),
  CONSTRAINT "bank_consents_period" CHECK (transactions_from IS NULL OR transactions_to IS NULL OR transactions_from <= transactions_to),
  CONSTRAINT "bank_consents_permissions_no_nulls" CHECK (array_position(permissions, NULL) IS NULL)
);

CREATE TABLE "bank_credentials" (
  "bank_consent_id" uuid PRIMARY KEY,
  "access_token_encrypted" bytea NOT NULL,
  "refresh_token_encrypted" bytea,
  "token_expires_at" timestamptz,
  "oauth_scopes" text[] NOT NULL DEFAULT ('{}'::text[]),
  "encryption_key_id" varchar(100) NOT NULL,
  "version" bigint NOT NULL DEFAULT 0,
  "created_at" timestamptz NOT NULL DEFAULT (now()),
  "updated_at" timestamptz NOT NULL DEFAULT (now()),
  CONSTRAINT "bank_credentials_version_nonnegative" CHECK (version >= 0),
  CONSTRAINT "bank_credentials_token_nonempty" CHECK (octet_length(access_token_encrypted) > 0),
  CONSTRAINT "bank_credentials_refresh_nonempty" CHECK (refresh_token_encrypted IS NULL OR octet_length(refresh_token_encrypted) > 0),
  CONSTRAINT "bank_credentials_key_nonempty" CHECK (length(btrim(encryption_key_id)) > 0),
  CONSTRAINT "bank_credentials_scopes_no_nulls" CHECK (array_position(oauth_scopes, NULL) IS NULL)
);

CREATE TABLE "bank_oauth_attempts" (
  "id" uuid PRIMARY KEY DEFAULT (gen_random_uuid()),
  "bank_connection_id" uuid NOT NULL,
  "bank_consent_id" uuid,
  "state_hash" varchar(64) UNIQUE NOT NULL,
  "pkce_verifier_encrypted" bytea NOT NULL,
  "encryption_key_id" varchar(100) NOT NULL,
  "nonce_hash" varchar(64),
  "expires_at" timestamptz NOT NULL,
  "consumed_at" timestamptz,
  "created_at" timestamptz NOT NULL DEFAULT (now()),
  CONSTRAINT "oauth_state_hash_format" CHECK (state_hash ~ '^[0-9a-f]{64}$'),
  CONSTRAINT "oauth_nonce_hash_format" CHECK (nonce_hash IS NULL OR nonce_hash ~ '^[0-9a-f]{64}$'),
  CONSTRAINT "oauth_expiry_after_creation" CHECK (expires_at > created_at),
  CONSTRAINT "oauth_consumed_after_creation" CHECK (consumed_at IS NULL OR consumed_at >= created_at),
  CONSTRAINT "oauth_pkce_nonempty" CHECK (octet_length(pkce_verifier_encrypted) > 0),
  CONSTRAINT "oauth_key_nonempty" CHECK (length(btrim(encryption_key_id)) > 0)
);

CREATE TABLE "accounts" (
  "id" uuid PRIMARY KEY DEFAULT (gen_random_uuid()),
  "user_id" uuid NOT NULL,
  "bank_id" uuid NOT NULL,
  "bank_connection_id" uuid,
  "external_account_id" varchar(255),
  "account_number" varchar(100),
  "name" varchar(200) NOT NULL,
  "type" text NOT NULL DEFAULT 'unknown',
  "status" text NOT NULL DEFAULT 'unknown',
  "is_archived" boolean NOT NULL DEFAULT false,
  "closed_at" timestamptz,
  "currency_code" varchar(3) NOT NULL,
  "reported_balance" numeric(20,4),
  "balance_as_of" timestamptz,
  "created_at" timestamptz NOT NULL DEFAULT (now()),
  "updated_at" timestamptz NOT NULL DEFAULT (now()),
  CONSTRAINT "accounts_type_allowed" CHECK (type IN ('current', 'credit_card', 'savings', 'deposit', 'unknown')),
  CONSTRAINT "accounts_status_allowed" CHECK (status IN ('active', 'closed', 'unknown')),
  CONSTRAINT "accounts_closed_date_status" CHECK (closed_at IS NULL OR status = 'closed'),
  CONSTRAINT "accounts_balance_pair" CHECK ((reported_balance IS NULL) = (balance_as_of IS NULL)),
  CONSTRAINT "accounts_external_requires_connection" CHECK (external_account_id IS NULL OR (bank_connection_id IS NOT NULL AND length(btrim(external_account_id)) > 0)),
  CONSTRAINT "accounts_number_nonempty" CHECK (account_number IS NULL OR length(btrim(account_number)) > 0),
  CONSTRAINT "accounts_identity_required" CHECK (account_number IS NOT NULL OR external_account_id IS NOT NULL),
  CONSTRAINT "accounts_balance_finite" CHECK (reported_balance IS NULL OR reported_balance NOT IN ('NaN'::numeric, 'Infinity'::numeric, '-Infinity'::numeric))
);

CREATE TABLE "statement_imports" (
  "id" uuid PRIMARY KEY DEFAULT (gen_random_uuid()),
  "account_id" uuid NOT NULL,
  "file_name" varchar(255) NOT NULL,
  "file_sha256" varchar(64) NOT NULL,
  "status" text NOT NULL DEFAULT 'pending',
  "imported_count" integer NOT NULL DEFAULT 0,
  "skipped_count" integer NOT NULL DEFAULT 0,
  "error_count" integer NOT NULL DEFAULT 0,
  "error_message" text,
  "completed_at" timestamptz,
  "created_at" timestamptz NOT NULL DEFAULT (now()),
  "updated_at" timestamptz NOT NULL DEFAULT (now()),
  CONSTRAINT "statement_imports_status_allowed" CHECK (status IN ('pending', 'processing', 'completed', 'failed')),
  CONSTRAINT "statement_imports_counts_nonnegative" CHECK (imported_count >= 0 AND skipped_count >= 0 AND error_count >= 0),
  CONSTRAINT "statement_imports_hash_format" CHECK (file_sha256 ~ '^[0-9a-f]{64}$')
);

CREATE TABLE "transactions" (
  "id" uuid PRIMARY KEY DEFAULT (gen_random_uuid()),
  "account_id" uuid NOT NULL,
  "statement_import_id" uuid,
  "external_transaction_id" varchar(255),
  "source" text NOT NULL,
  "status" text NOT NULL,
  "raw_bank_status" varchar(255),
  "amount" numeric(20,4) NOT NULL,
  "currency_code" varchar(3) NOT NULL,
  "original_amount" numeric(20,4),
  "original_currency_code" varchar(3),
  "occurred_at" timestamptz NOT NULL,
  "booked_at" timestamptz,
  "value_at" timestamptz,
  "description" text,
  "counterparty_name" varchar(255),
  "counterparty_account" varchar(100),
  "merchant_name" varchar(350),
  "mcc" varchar(4),
  "category_id" smallint,
  "is_category_manual" boolean NOT NULL DEFAULT false,
  "deduplication_key" varchar(64) NOT NULL,
  "row_version" bigint NOT NULL DEFAULT 0,
  "created_at" timestamptz NOT NULL DEFAULT (now()),
  "updated_at" timestamptz NOT NULL DEFAULT (now()),
  CONSTRAINT "transactions_source_allowed" CHECK (source IN ('open_banking', 'statement')),
  CONSTRAINT "transactions_status_allowed" CHECK (status IN ('pending', 'booked', 'rejected')),
  CONSTRAINT "transactions_date_required" CHECK (booked_at IS NOT NULL OR value_at IS NOT NULL),
  CONSTRAINT "transactions_booked_date_required" CHECK (status <> 'booked' OR booked_at IS NOT NULL),
  CONSTRAINT "transactions_original_pair" CHECK ((original_amount IS NULL) = (original_currency_code IS NULL)),
  CONSTRAINT "transactions_original_direction" CHECK (original_amount IS NULL OR sign(amount) = sign(original_amount)),
  CONSTRAINT "transactions_mcc_format" CHECK (mcc IS NULL OR mcc ~ '^[0-9]{4}$'),
  CONSTRAINT "transactions_external_nonempty" CHECK (external_transaction_id IS NULL OR length(btrim(external_transaction_id)) > 0),
  CONSTRAINT "transactions_dedup_format" CHECK (deduplication_key ~ '^[0-9a-f]{64}$'),
  CONSTRAINT "transactions_version_nonnegative" CHECK (row_version >= 0),
  CONSTRAINT "transactions_amount_finite" CHECK (amount NOT IN ('NaN'::numeric, 'Infinity'::numeric, '-Infinity'::numeric)),
  CONSTRAINT "transactions_original_finite" CHECK (original_amount IS NULL OR original_amount NOT IN ('NaN'::numeric, 'Infinity'::numeric, '-Infinity'::numeric))
);

CREATE TABLE "exchange_rates" (
  "currency_code" varchar(3) NOT NULL,
  "rate_date" date NOT NULL,
  "rub_per_unit" numeric(24,10) NOT NULL,
  "fetched_at" timestamptz NOT NULL DEFAULT (now()),
  CONSTRAINT "exchange_rates_positive_finite" CHECK (rub_per_unit > 0 AND rub_per_unit NOT IN ('NaN'::numeric, 'Infinity'::numeric, '-Infinity'::numeric)),
  PRIMARY KEY ("currency_code", "rate_date")
);

CREATE TABLE "audit_logs" (
  "logged_at" timestamptz NOT NULL DEFAULT (now()),
  "id" uuid NOT NULL DEFAULT (gen_random_uuid()),
  "service" varchar(50) NOT NULL,
  "event_code" varchar(100) NOT NULL,
  "outcome" text NOT NULL,
  "message" text NOT NULL,
  "request_id" uuid,
  "user_id" uuid,
  "entity_type" varchar(100),
  "entity_id" text,
  "details" jsonb NOT NULL DEFAULT ('{}'::jsonb),
  CONSTRAINT "audit_logs_outcome_allowed" CHECK (outcome IN ('success', 'failure')),
  CONSTRAINT "audit_logs_details_object" CHECK (jsonb_typeof(details) = 'object'),
  CONSTRAINT "audit_logs_entity_pair" CHECK ((entity_type IS NULL) = (entity_id IS NULL)),
  PRIMARY KEY ("logged_at", "id")
);

CREATE UNIQUE INDEX "users_email_lower_uq" ON "users" ((lower(email)));

CREATE UNIQUE INDEX "bank_connections_user_bank_uq" ON "bank_connections" ("user_id", "bank_id");

CREATE UNIQUE INDEX "bank_connections_owner_uq" ON "bank_connections" ("id", "user_id", "bank_id");

CREATE UNIQUE INDEX "bank_connections_bank_uq" ON "bank_connections" ("id", "bank_id");

CREATE INDEX ON "bank_connections" ("bank_id");

CREATE UNIQUE INDEX "bank_consents_external_uq" ON "bank_consents" ("bank_id", "external_consent_id");

CREATE INDEX ON "bank_consents" ("bank_connection_id");

CREATE INDEX ON "bank_consents" ("expires_at");

CREATE INDEX ON "bank_oauth_attempts" ("bank_connection_id");

CREATE INDEX ON "bank_oauth_attempts" ("bank_consent_id");

CREATE INDEX ON "bank_oauth_attempts" ("expires_at");

CREATE UNIQUE INDEX "accounts_number_uq" ON "accounts" ("user_id", "bank_id", "account_number", "currency_code");

CREATE UNIQUE INDEX "accounts_currency_uq" ON "accounts" ("id", "currency_code");

CREATE INDEX ON "accounts" ("bank_id");

CREATE INDEX ON "accounts" ("bank_connection_id");

CREATE INDEX ON "statement_imports" ("account_id");

CREATE UNIQUE INDEX "statement_imports_account_uq" ON "statement_imports" ("id", "account_id");

CREATE UNIQUE INDEX "transactions_dedup_uq" ON "transactions" ("account_id", "deduplication_key");

CREATE INDEX "transactions_feed_idx" ON "transactions" ("account_id", "occurred_at", "id");

CREATE INDEX "transactions_booked_idx" ON "transactions" ("account_id", "booked_at", "id");

CREATE INDEX ON "transactions" ("statement_import_id");

CREATE INDEX ON "transactions" ("category_id");

CREATE INDEX ON "transactions" ("original_currency_code");

CREATE INDEX ON "exchange_rates" ("rate_date");

CREATE INDEX ON "audit_logs" ("user_id", "logged_at");

CREATE INDEX ON "audit_logs" ("event_code", "logged_at");

CREATE INDEX ON "audit_logs" ("request_id");

COMMENT ON TABLE "transaction_categories" IS 'Общие категории MVP. NULL category_id в операции означает отсутствие категории.';

COMMENT ON TABLE "banks" IS 'code совпадает с ключом банка в конфигурации. bic — российский БИК; у Mock Bank может отсутствовать. URL и секреты не хранятся в этой таблице.';

COMMENT ON TABLE "users" IS 'Наличие модели пользователя не добавляет регистрацию и вход в текущий демонстрационный HTTP API.';

COMMENT ON COLUMN "users"."password_hash" IS 'Только хеш. Вход через внешний IdP пока не моделируется; нельзя просто разрешить NULL без альтернативной учётной записи.';

COMMENT ON TABLE "bank_connections" IS 'MVP: одно подключение пользователя к банку, один внешний профиль клиента. external_customer_id — ID внешней системы, не FK на users. Частичный индекс next_sync_at для active задан в SQL. Обновления конкурирующих писателей: WHERE version = ожидаемая_версия; триггер повышает version.';

COMMENT ON COLUMN "bank_connections"."last_sync_error" IS 'Без токенов, банковских реквизитов и персональных данных';

COMMENT ON TABLE "bank_consents" IS 'Разрешения Open Banking не равны OAuth scopes. Маппинг и проверка разрешений — в адаптере/usecase. SQL разрешает не более одного authorised-согласия на подключение: при замене старое закрывают в той же транзакции. Время истечения и ответ банка проверяются отдельно; CHECK не подтверждает получение ответа банка.';

COMMENT ON TABLE "bank_credentials" IS 'У согласия 0 или 1 запись токенов. Ключ шифрования — вне БД. Конкурентная ротация: UPDATE с WHERE version = ожидаемая_версия; SQL-триггер повышает version.';

COMMENT ON TABLE "bank_oauth_attempts" IS 'Без Redis в MVP. Регулярно удалять истёкшие попытки. SQL задаёт частичный UNIQUE nonce_hash и проверяет принадлежность согласия подключению. Callback однократно потребляет state атомарным UPDATE и проверяет текущую сессию. В БД нет открытых state, nonce и PKCE verifier.';

COMMENT ON COLUMN "bank_oauth_attempts"."bank_consent_id" IS 'NULL до создания согласия; привязка к подключению обязательна всегда';

COMMENT ON TABLE "accounts" IS 'Счёт из выписки может не иметь подключения. Составной FK сохраняет владельца и банк. SQL: UNIQUE (bank_id, external_account_id) WHERE external_account_id IS NOT NULL. MVP не поддерживает один внешний счёт у нескольких локальных владельцев. Сопоставлять выписку и API только по надёжным реквизитам, не по имени/балансу. Неизвестный остаток — NULL. Архивирование пользователем независимо от статуса банка.';

COMMENT ON COLUMN "accounts"."account_number" IS 'Может отсутствовать в API; не подставлять фиктивный номер';

COMMENT ON TABLE "statement_imports" IS 'SQL: UNIQUE (account_id, file_sha256) WHERE status <> failed. Неудачный файл можно загрузить новой попыткой. Операции и completed фиксируются одной транзакцией; failed не должен оставлять частично записанные операции.';

COMMENT ON TABLE "transactions" IS 'SQL: UNIQUE (account_id, external_transaction_id) WHERE external_transaction_id IS NOT NULL; триггер запрещает замену присвоенного ID и перенос операции на другой счёт. NULL -> ID разрешено один раз после надёжного сопоставления. deduplication_key не строить только из суммы/даты: разные платежи нельзя склеивать. is_category_manual защищает ручной выбор и ручную очистку категории; синк не меняет их. Курсы для booked-аналитики выбираются по booked_at. Если банк не дал ни booked_at, ни value_at, запись не принимается — это явное ограничение MVP.';

COMMENT ON COLUMN "transactions"."raw_bank_status" IS 'Как пришло от банка. Без FK; адаптер преобразует статус в наш. Неизвестный статус нельзя молча считать booked.';

COMMENT ON COLUMN "transactions"."amount" IS 'В валюте счёта: credit — плюс, debit — минус, ноль допустим';

COMMENT ON COLUMN "transactions"."occurred_at" IS 'Время операции для ленты. Не now() при синхронизации; после сохранения не меняется, даже при pending -> booked.';

COMMENT ON TABLE "exchange_rates" IS 'Курс одной единицы валюты к RUB на фактическую дату действия; номинал уже учтён. Для RUB — 1. Валюта БД currency_code может называться currency в JSON.';

COMMENT ON TABLE "audit_logs" IS 'Только аудит бизнес-действий. Технические логи — stdout. PK совместим с будущим партиционированием по logged_at, сами партиции в MVP не создаются. Удаление пользователя не переписывает аудит. Политика хранения и удаления персональных данных определяется отдельно.';

COMMENT ON COLUMN "audit_logs"."user_id" IS 'Снимок ID действовавшего пользователя, намеренно без FK';

COMMENT ON COLUMN "audit_logs"."details" IS 'Дополнительные поля произвольного состава; без паролей, токенов и необработанных банковских ответов';

ALTER TABLE "bank_connections" ADD FOREIGN KEY ("user_id") REFERENCES "users" ("id") ON DELETE RESTRICT ON UPDATE RESTRICT;

ALTER TABLE "bank_connections" ADD FOREIGN KEY ("bank_id") REFERENCES "banks" ("id") ON DELETE RESTRICT ON UPDATE RESTRICT;

ALTER TABLE "bank_consents" ADD FOREIGN KEY ("bank_connection_id", "bank_id") REFERENCES "bank_connections" ("id", "bank_id") ON DELETE RESTRICT ON UPDATE RESTRICT;

ALTER TABLE "bank_credentials" ADD FOREIGN KEY ("bank_consent_id") REFERENCES "bank_consents" ("id") ON DELETE CASCADE ON UPDATE RESTRICT;

ALTER TABLE "bank_oauth_attempts" ADD FOREIGN KEY ("bank_connection_id") REFERENCES "bank_connections" ("id") ON DELETE CASCADE ON UPDATE RESTRICT;

ALTER TABLE "bank_oauth_attempts" ADD FOREIGN KEY ("bank_consent_id") REFERENCES "bank_consents" ("id") ON DELETE CASCADE ON UPDATE RESTRICT;

ALTER TABLE "accounts" ADD FOREIGN KEY ("user_id") REFERENCES "users" ("id") ON DELETE RESTRICT ON UPDATE RESTRICT;

ALTER TABLE "accounts" ADD FOREIGN KEY ("bank_id") REFERENCES "banks" ("id") ON DELETE RESTRICT ON UPDATE RESTRICT;

ALTER TABLE "accounts" ADD FOREIGN KEY ("currency_code") REFERENCES "currencies" ("code") ON DELETE RESTRICT ON UPDATE RESTRICT;

ALTER TABLE "accounts" ADD FOREIGN KEY ("bank_connection_id", "user_id", "bank_id") REFERENCES "bank_connections" ("id", "user_id", "bank_id") ON DELETE RESTRICT ON UPDATE RESTRICT;

ALTER TABLE "statement_imports" ADD FOREIGN KEY ("account_id") REFERENCES "accounts" ("id") ON DELETE RESTRICT ON UPDATE RESTRICT;

ALTER TABLE "transactions" ADD FOREIGN KEY ("account_id", "currency_code") REFERENCES "accounts" ("id", "currency_code") ON DELETE RESTRICT ON UPDATE RESTRICT;

ALTER TABLE "transactions" ADD FOREIGN KEY ("statement_import_id", "account_id") REFERENCES "statement_imports" ("id", "account_id") ON DELETE RESTRICT ON UPDATE RESTRICT;

ALTER TABLE "transactions" ADD FOREIGN KEY ("category_id") REFERENCES "transaction_categories" ("id") ON DELETE RESTRICT ON UPDATE RESTRICT;

ALTER TABLE "transactions" ADD FOREIGN KEY ("original_currency_code") REFERENCES "currencies" ("code") ON DELETE RESTRICT ON UPDATE RESTRICT;

ALTER TABLE "exchange_rates" ADD FOREIGN KEY ("currency_code") REFERENCES "currencies" ("code") ON DELETE RESTRICT ON UPDATE RESTRICT;


CREATE UNIQUE INDEX accounts_external_uq
 ON accounts (bank_id, external_account_id)
 WHERE external_account_id IS NOT NULL;

CREATE UNIQUE INDEX bank_connections_customer_uq
 ON bank_connections (bank_id, external_customer_id)
 WHERE external_customer_id IS NOT NULL;

CREATE UNIQUE INDEX transactions_external_uq
 ON transactions (account_id, external_transaction_id)
 WHERE external_transaction_id IS NOT NULL;

CREATE UNIQUE INDEX oauth_nonce_hash_uq
 ON bank_oauth_attempts (nonce_hash)
 WHERE nonce_hash IS NOT NULL;

CREATE UNIQUE INDEX statement_imports_file_uq
 ON statement_imports (account_id, file_sha256)
 WHERE status <> 'failed';

CREATE UNIQUE INDEX bank_consents_one_authorised_uq
 ON bank_consents (bank_connection_id)
 WHERE status = 'authorised';

CREATE INDEX bank_connections_due_sync_idx
 ON bank_connections (next_sync_at)
 WHERE status = 'active' AND next_sync_at IS NOT NULL;

CREATE INDEX transactions_booked_analytics_idx
 ON transactions (account_id, booked_at DESC, id DESC)
 WHERE status = 'booked';

-- Активность согласия не определяется только expires_at или только status.
-- При замене согласия старое нужно перевести в revoked/expired до активации
-- нового, в той же транзакции. Не включать now() в предикат индекса.

CREATE FUNCTION touch_updated_at()
RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
  NEW.updated_at := clock_timestamp();
  RETURN NEW;
END;
$$;

DO $$
DECLARE target_table text;
BEGIN
  FOREACH target_table IN ARRAY ARRAY[
    'users', 'banks', 'accounts', 'bank_consents', 'statement_imports'
  ] LOOP
    EXECUTE format(
      'CREATE TRIGGER touch_updated_at BEFORE UPDATE ON %I FOR EACH ROW EXECUTE FUNCTION touch_updated_at()',
      target_table
    );
  END LOOP;
END;
$$;

CREATE FUNCTION guard_transaction_identity()
RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
  IF NEW.id IS DISTINCT FROM OLD.id
     OR NEW.account_id IS DISTINCT FROM OLD.account_id
     OR NEW.source IS DISTINCT FROM OLD.source
     OR NEW.deduplication_key IS DISTINCT FROM OLD.deduplication_key
     OR NEW.occurred_at IS DISTINCT FROM OLD.occurred_at THEN
    RAISE EXCEPTION 'Transaction identity, initial source and feed time are immutable'
      USING ERRCODE = '23514';
  END IF;
  IF OLD.external_transaction_id IS NOT NULL
     AND NEW.external_transaction_id IS DISTINCT FROM OLD.external_transaction_id THEN
    RAISE EXCEPTION 'Assigned external_transaction_id cannot be replaced or cleared'
      USING ERRCODE = '23514';
  END IF;
  NEW.row_version := OLD.row_version + 1;
  NEW.updated_at := clock_timestamp();
  RETURN NEW;
END;
$$;

CREATE TRIGGER guard_transaction_identity BEFORE UPDATE ON transactions
 FOR EACH ROW EXECUTE FUNCTION guard_transaction_identity();

CREATE FUNCTION guard_account_identity()
RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
  IF NEW.id IS DISTINCT FROM OLD.id
     OR NEW.user_id IS DISTINCT FROM OLD.user_id
     OR NEW.bank_id IS DISTINCT FROM OLD.bank_id
     OR NEW.currency_code IS DISTINCT FROM OLD.currency_code THEN
    RAISE EXCEPTION 'Account identity, ownership and currency are immutable'
      USING ERRCODE = '23514';
  END IF;
  IF (OLD.bank_connection_id IS NOT NULL AND NEW.bank_connection_id IS DISTINCT FROM OLD.bank_connection_id)
     OR (OLD.external_account_id IS NOT NULL AND NEW.external_account_id IS DISTINCT FROM OLD.external_account_id)
     OR (OLD.account_number IS NOT NULL AND NEW.account_number IS DISTINCT FROM OLD.account_number) THEN
    RAISE EXCEPTION 'Assigned account identifiers cannot be replaced or cleared'
      USING ERRCODE = '23514';
  END IF;
  RETURN NEW;
END;
$$;

CREATE TRIGGER guard_account_identity BEFORE UPDATE ON accounts
 FOR EACH ROW EXECUTE FUNCTION guard_account_identity();

CREATE FUNCTION guard_bank_connection_identity()
RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
  IF NEW.id IS DISTINCT FROM OLD.id
     OR NEW.user_id IS DISTINCT FROM OLD.user_id
     OR NEW.bank_id IS DISTINCT FROM OLD.bank_id
     OR (OLD.external_customer_id IS NOT NULL AND NEW.external_customer_id IS DISTINCT FROM OLD.external_customer_id) THEN
    RAISE EXCEPTION 'Bank connection identity and ownership are immutable'
      USING ERRCODE = '23514';
  END IF;
  NEW.version := OLD.version + 1;
  NEW.updated_at := clock_timestamp();
  RETURN NEW;
END;
$$;

CREATE TRIGGER guard_bank_connection_identity BEFORE UPDATE ON bank_connections
 FOR EACH ROW EXECUTE FUNCTION guard_bank_connection_identity();

CREATE FUNCTION guard_bank_consent_identity()
RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
  IF NEW.id IS DISTINCT FROM OLD.id
     OR NEW.bank_connection_id IS DISTINCT FROM OLD.bank_connection_id
     OR NEW.bank_id IS DISTINCT FROM OLD.bank_id
     OR NEW.external_consent_id IS DISTINCT FROM OLD.external_consent_id THEN
    RAISE EXCEPTION 'Bank consent identity is immutable'
      USING ERRCODE = '23514';
  END IF;
  RETURN NEW;
END;
$$;

CREATE TRIGGER guard_bank_consent_identity BEFORE UPDATE ON bank_consents
 FOR EACH ROW EXECUTE FUNCTION guard_bank_consent_identity();

CREATE FUNCTION guard_credentials_identity()
RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
  IF NEW.bank_consent_id IS DISTINCT FROM OLD.bank_consent_id THEN
    RAISE EXCEPTION 'Credential consent is immutable' USING ERRCODE = '23514';
  END IF;
  NEW.version := OLD.version + 1;
  NEW.updated_at := clock_timestamp();
  RETURN NEW;
END;
$$;

CREATE TRIGGER guard_credentials_identity BEFORE UPDATE ON bank_credentials
 FOR EACH ROW EXECUTE FUNCTION guard_credentials_identity();

CREATE FUNCTION check_oauth_consent_connection()
RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
  IF NEW.bank_consent_id IS NOT NULL THEN
    PERFORM 1 FROM bank_consents
    WHERE id = NEW.bank_consent_id AND bank_connection_id = NEW.bank_connection_id;
    IF NOT FOUND THEN
      RAISE EXCEPTION 'OAuth consent must belong to the same connection'
        USING ERRCODE = '23514';
    END IF;
  END IF;
  RETURN NEW;
END;
$$;

CREATE TRIGGER check_oauth_consent_connection
 BEFORE INSERT OR UPDATE ON bank_oauth_attempts
 FOR EACH ROW EXECUTE FUNCTION check_oauth_consent_connection();

COMMIT;
