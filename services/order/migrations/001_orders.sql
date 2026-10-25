CREATE TABLE purchase_intents (
 shopper_id uuid NOT NULL,
 request_key text NOT NULL CHECK(length(request_key) BETWEEN 1 AND 128),
 payload_hash text NOT NULL,
 purchase jsonb NOT NULL,
 order_id uuid NOT NULL UNIQUE,
 original_attempt_id uuid NOT NULL,
 correlation_id uuid NOT NULL,
 state text NOT NULL CHECK(state IN ('RECEIVED','RESERVING','ACCEPTED','REJECTED')),
 reservation_id uuid,
 rejection_code text,
 response_snapshot jsonb,
 trusted_offer jsonb,
 lease_owner uuid,
 lease_until timestamptz,
 created_at timestamptz NOT NULL,
 updated_at timestamptz NOT NULL,
 PRIMARY KEY(shopper_id,request_key)
);
CREATE TABLE orders (
 order_id uuid PRIMARY KEY REFERENCES purchase_intents(order_id),
 shopper_id uuid NOT NULL,
 sale_id uuid NOT NULL,
 product_id uuid NOT NULL,
 reservation_id uuid NOT NULL UNIQUE,
 payment_attempt_id uuid NOT NULL UNIQUE,
 refund_id uuid,
 public_state text NOT NULL CHECK(public_state IN ('PENDING_PAYMENT','CONFIRMED','FAILED','EXPIRED')),
 workflow_step text NOT NULL,
 aggregate_version bigint NOT NULL CHECK(aggregate_version>0),
 snapshot jsonb NOT NULL,
 payment_success jsonb,
 payment_version bigint,
 updated_at timestamptz NOT NULL
);
CREATE INDEX orders_shopper ON orders(shopper_id,order_id);
