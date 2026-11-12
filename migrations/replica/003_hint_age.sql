BEGIN;
SELECT pg_advisory_xact_lock(1);
SET LOCAL ROLE replica_app;
ALTER TABLE replica.hints ADD COLUMN IF NOT EXISTS created_at timestamptz NOT NULL DEFAULT clock_timestamp();
INSERT INTO replica.schema_version (version) VALUES (3) ON CONFLICT DO NOTHING;
COMMIT;
