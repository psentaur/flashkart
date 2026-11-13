BEGIN;
SELECT pg_advisory_xact_lock(1);
DO $$
DECLARE source text;
BEGIN
    FOREACH source IN ARRAY ARRAY['admission.events','inventory.events','inventory.outcomes','orders.events','orders.outcomes','payment.events'] LOOP
        EXECUTE format('ALTER TABLE %s ADD COLUMN IF NOT EXISTS delivered_at timestamptz,
            ADD COLUMN IF NOT EXISTS next_attempt_at timestamptz NOT NULL DEFAULT clock_timestamp(),
            ADD COLUMN IF NOT EXISTS delivery_attempts integer NOT NULL DEFAULT 0,
            ADD COLUMN IF NOT EXISTS dead_at timestamptz', source);
        EXECUTE format('CREATE INDEX IF NOT EXISTS %I ON %s (next_attempt_at,event_id) WHERE delivered_at IS NULL AND dead_at IS NULL',
            split_part(source,'.',2)||'_delivery_pending', source);
    END LOOP;
END $$;
COMMIT;
