BEGIN;
SELECT pg_advisory_xact_lock(1);
SET LOCAL ROLE flash_sale_app;
CREATE TABLE IF NOT EXISTS flash_sale.drafts (
    sale_id text PRIMARY KEY,
    terms jsonb NOT NULL,
    state text NOT NULL DEFAULT 'DRAFT' CHECK (state IN ('DRAFT','PENDING','PUBLISHED')),
    operation_id text UNIQUE,
    origin text CHECK (origin IN ('a','b','c','d')),
    step smallint NOT NULL DEFAULT 0 CHECK (step BETWEEN 0 AND 4),
    envelope bytea,
    next_attempt_at timestamptz NOT NULL DEFAULT clock_timestamp(),
    correlation_id text,
    CHECK ((state = 'DRAFT') = (operation_id IS NULL)),
    CHECK ((operation_id IS NULL) = (origin IS NULL)),
    CHECK ((step >= 2) = (envelope IS NOT NULL)),
    CHECK ((step = 4) = (state = 'PUBLISHED'))
);
CREATE INDEX IF NOT EXISTS drafts_pending ON flash_sale.drafts (next_attempt_at, sale_id) WHERE state='PENDING';
RESET ROLE;
SET LOCAL ROLE inventory_app;
CREATE TABLE IF NOT EXISTS inventory.sales (
    sale_id text PRIMARY KEY,
    operation_id text NOT NULL UNIQUE,
    terms jsonb NOT NULL,
    available_stock bigint NOT NULL CHECK (available_stock >= 0),
    active boolean NOT NULL DEFAULT false
);
COMMIT;
