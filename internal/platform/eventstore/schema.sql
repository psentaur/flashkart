CREATE TABLE IF NOT EXISTS platform_inbox (
 consumer_name text NOT NULL,event_id uuid NOT NULL,payload_hash bytea NOT NULL,
 processed_at timestamptz NOT NULL DEFAULT now(),PRIMARY KEY(consumer_name,event_id)
);
CREATE TABLE IF NOT EXISTS platform_outbox (
 event_id uuid PRIMARY KEY,aggregate_id uuid NOT NULL,aggregate_version bigint NOT NULL CHECK(aggregate_version>0),
 envelope bytea NOT NULL,created_at timestamptz NOT NULL DEFAULT now()
);
CREATE TABLE IF NOT EXISTS platform_delivery (
 event_id uuid NOT NULL REFERENCES platform_outbox(event_id) ON DELETE CASCADE,
 destination text NOT NULL,record_key text NOT NULL,published_at timestamptz,
 attempts integer NOT NULL DEFAULT 0,next_attempt_at timestamptz NOT NULL DEFAULT now(),
 PRIMARY KEY(event_id,destination)
);
CREATE TABLE IF NOT EXISTS platform_failed_work (
 id bigserial PRIMARY KEY,consumer_name text NOT NULL,event_id text NOT NULL,original_topic text NOT NULL,
 original_partition integer NOT NULL,original_offset bigint NOT NULL,record_key bytea NOT NULL,payload bytea NOT NULL,
 headers jsonb NOT NULL DEFAULT '[]',error_code text NOT NULL,attempts integer NOT NULL DEFAULT 0,
 state text NOT NULL CHECK(state IN ('RETRY','PAUSED','QUARANTINED','REPLAYED')),
 next_attempt_at timestamptz NOT NULL,first_seen_at timestamptz NOT NULL,last_seen_at timestamptz NOT NULL,
 UNIQUE(consumer_name,original_topic,original_partition,original_offset)
);
CREATE TABLE IF NOT EXISTS platform_replay_audit (
 id bigserial PRIMARY KEY,failed_work_id bigint NOT NULL REFERENCES platform_failed_work(id),
 operator_id uuid NOT NULL,reason text NOT NULL CHECK(length(reason)>0),replayed_at timestamptz NOT NULL
);
