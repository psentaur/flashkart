BEGIN;
SELECT pg_advisory_xact_lock(1);
SET LOCAL ROLE admission_app;
CREATE TABLE IF NOT EXISTS admission.attempts (
    attempt_id text PRIMARY KEY,
    sale_id text NOT NULL,
    customer_id text NOT NULL
);
CREATE TABLE IF NOT EXISTS admission.purchase_outbox (
    attempt_id text PRIMARY KEY REFERENCES admission.attempts,
    body jsonb NOT NULL,
    next_attempt_at timestamptz NOT NULL DEFAULT clock_timestamp(),
    sent_at timestamptz
);
CREATE INDEX IF NOT EXISTS purchase_outbox_pending
    ON admission.purchase_outbox(next_attempt_at, attempt_id) WHERE sent_at IS NULL;
CREATE TABLE IF NOT EXISTS admission.events (
    event_id text PRIMARY KEY,
    body jsonb NOT NULL
);
RESET ROLE;
SET LOCAL ROLE inventory_app;
CREATE TABLE IF NOT EXISTS inventory.attempts (
    attempt_id text PRIMARY KEY,
    sale_id text NOT NULL,
    customer_id text NOT NULL,
    rejection text CHECK (rejection IN ('INACTIVE','ENDED','SOLD_OUT'))
);
CREATE TABLE IF NOT EXISTS inventory.reservations (
    reservation_id text PRIMARY KEY REFERENCES inventory.attempts(attempt_id),
    sale_id text NOT NULL REFERENCES inventory.sales,
    customer_id text NOT NULL,
    trace_id text NOT NULL,
    expires_at timestamptz NOT NULL,
    state text NOT NULL CHECK (state IN ('HELD','COMMITTED','RELEASED','EXPIRED'))
);
CREATE TABLE IF NOT EXISTS inventory.order_work (
    reservation_id text PRIMARY KEY REFERENCES inventory.reservations,
    body jsonb NOT NULL
);
CREATE TABLE IF NOT EXISTS inventory.events (
    event_id text PRIMARY KEY,
    body jsonb NOT NULL
);
COMMIT;
