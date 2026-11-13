BEGIN;
SELECT pg_advisory_xact_lock(1);
SET LOCAL ROLE inventory_app;
ALTER TABLE inventory.order_work ADD COLUMN IF NOT EXISTS delivered_at timestamptz;
ALTER TABLE inventory.order_work ADD COLUMN IF NOT EXISTS next_attempt_at timestamptz NOT NULL DEFAULT clock_timestamp();
CREATE INDEX IF NOT EXISTS order_work_pending ON inventory.order_work(next_attempt_at,reservation_id) WHERE delivered_at IS NULL;
CREATE INDEX IF NOT EXISTS reservations_expiring ON inventory.reservations(expires_at,reservation_id) WHERE state='HELD';
CREATE TABLE IF NOT EXISTS inventory.outcomes (event_id text PRIMARY KEY, body jsonb NOT NULL);
RESET ROLE;
SET LOCAL ROLE orders_app;
CREATE TABLE IF NOT EXISTS orders.purchases (
    order_id text PRIMARY KEY,
    customer_id text NOT NULL,
    request bytea NOT NULL,
    state text NOT NULL DEFAULT 'PENDING' CHECK (state IN ('PENDING','CONFIRMED','CANCELLED')),
    created_at timestamptz NOT NULL DEFAULT clock_timestamp(),
    next_attempt_at timestamptz NOT NULL DEFAULT clock_timestamp()
);
CREATE INDEX IF NOT EXISTS purchases_pending ON orders.purchases(next_attempt_at,order_id) WHERE state='PENDING';
CREATE TABLE IF NOT EXISTS orders.events (event_id text PRIMARY KEY, body jsonb NOT NULL);
CREATE TABLE IF NOT EXISTS orders.outcomes (event_id text PRIMARY KEY, body jsonb NOT NULL);
RESET ROLE;
SET LOCAL ROLE payment_app;
CREATE TABLE IF NOT EXISTS payment.transactions (
    payment_id text PRIMARY KEY,
    request bytea NOT NULL,
    state text NOT NULL CHECK (state IN ('PENDING','SUCCEEDED','DECLINED','REFUNDED')),
    decision text NOT NULL CHECK (decision IN ('SUCCEEDED','DECLINED')),
    due_at timestamptz NOT NULL,
    lose_reply boolean NOT NULL
);
CREATE TABLE IF NOT EXISTS payment.events (event_id text PRIMARY KEY, body jsonb NOT NULL);
COMMIT;
