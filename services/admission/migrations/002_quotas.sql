-- One SALE lock serializes quotas and live lease counts across Admission replicas.
CREATE TABLE admission_quotas (
 scope text NOT NULL CHECK(scope IN ('SALE','SHOPPER_SALE')),
 scope_id text NOT NULL,
 bucket_start timestamptz NOT NULL,
 count bigint NOT NULL DEFAULT 0 CHECK(count>=0),
 quota_limit bigint NOT NULL CHECK(quota_limit>0),
 interval_ms integer NOT NULL DEFAULT 1000 CHECK(interval_ms=1000),
 PRIMARY KEY(scope,scope_id)
);
CREATE TABLE admission_leases (
 attempt_id uuid PRIMARY KEY,
 sale_id uuid NOT NULL,
 shopper_id uuid NOT NULL,
 expires_at timestamptz NOT NULL,
 released_at timestamptz
);
CREATE INDEX admission_live_leases ON admission_leases(sale_id,expires_at) WHERE released_at IS NULL;
CREATE TABLE purchase_attempts (
 attempt_id uuid PRIMARY KEY,
 shopper_id uuid NOT NULL,
 sale_id uuid NOT NULL,
 product_id uuid NOT NULL,
 received_at timestamptz NOT NULL,
 decision text NOT NULL CHECK(decision IN ('ADMITTED','REJECTED')),
 reason text NOT NULL,
 retry_after_seconds integer,
 response_status integer NOT NULL,
 response_error_code text,
 response_latency_ms bigint NOT NULL CHECK(response_latency_ms>=0),
 request_key_digest text NOT NULL,
 payload_hash text NOT NULL,
 response_snapshot jsonb NOT NULL,
 response_headers jsonb NOT NULL,
 CHECK(response_status<400 OR response_error_code IS NOT NULL)
);
-- Only trusted transport redelivery can reuse an attempt; each public retry gets a new ID.
CREATE TABLE admission_requests (
 attempt_id uuid PRIMARY KEY,
 payload_hash text NOT NULL,
 work_owner uuid NOT NULL,
 lease_until timestamptz NOT NULL
);
