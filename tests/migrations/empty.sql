DO $$
BEGIN
  IF EXISTS (
    SELECT 1 FROM pg_tables
    WHERE schemaname=current_schema() AND tablename <> 'schema_migrations'
  ) THEN RAISE EXCEPTION 'Down migration left application tables'; END IF;
  IF EXISTS (
    SELECT 1 FROM pg_proc p JOIN pg_namespace n ON n.oid=p.pronamespace
    WHERE n.nspname=current_schema()
      AND p.proname IN ('touch_updated_at','guard_transaction_identity','guard_account_identity',
        'guard_bank_connection_identity','guard_bank_consent_identity',
        'guard_credentials_identity','check_oauth_consent_connection')
  ) THEN RAISE EXCEPTION 'Down migration left trigger functions'; END IF;
END;
$$;
