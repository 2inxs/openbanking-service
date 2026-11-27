BEGIN;
CREATE TABLE rate_refresh_leases (
 job_key text PRIMARY KEY,
 owner uuid NOT NULL,
 version bigint NOT NULL CHECK (version > 0),
 lease_until timestamptz NOT NULL,
 next_attempt_at timestamptz NOT NULL,
 last_success_at timestamptz
);
COMMENT ON TABLE rate_refresh_leases IS 'Shared CBR refresh scheduling; DB clock, expiring ownership and fencing version. HTTP runs outside SQL transactions.';
COMMIT;
