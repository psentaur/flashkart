BEGIN;
SELECT pg_advisory_xact_lock(1);
CREATE SCHEMA IF NOT EXISTS replica AUTHORIZATION replica_app;
SET LOCAL ROLE replica_app;
CREATE TABLE IF NOT EXISTS replica.schema_version (
    version integer PRIMARY KEY
);
CREATE TABLE IF NOT EXISTS replica.documents (
    kind integer NOT NULL CHECK (kind IN (1, 2)),
    document_id text NOT NULL,
    counter bigint NOT NULL DEFAULT 0 CHECK (counter >= 0),
    PRIMARY KEY (kind, document_id)
);
CREATE TABLE IF NOT EXISTS replica.prepared (
    operation_id text PRIMARY KEY,
    request bytea NOT NULL,
    envelope bytea NOT NULL
);
CREATE TABLE IF NOT EXISTS replica.versions (
    kind integer NOT NULL,
    document_id text NOT NULL,
    origin text NOT NULL CHECK (origin IN ('a', 'b', 'c', 'd')),
    counter bigint NOT NULL CHECK (counter > 0),
    operation_id text NOT NULL UNIQUE,
    envelope bytea NOT NULL,
    visible boolean NOT NULL DEFAULT true,
    PRIMARY KEY (kind, document_id, origin, counter),
    FOREIGN KEY (kind, document_id) REFERENCES replica.documents
);
CREATE INDEX IF NOT EXISTS versions_visible
    ON replica.versions (kind, document_id) WHERE visible;
CREATE TABLE IF NOT EXISTS replica.hints (
    kind integer NOT NULL,
    document_id text NOT NULL,
    origin text NOT NULL,
    counter bigint NOT NULL,
    target text NOT NULL CHECK (target IN ('a', 'b', 'c', 'd')),
    PRIMARY KEY (kind, document_id, origin, counter, target),
    FOREIGN KEY (kind, document_id, origin, counter) REFERENCES replica.versions
);
INSERT INTO replica.schema_version (version) VALUES (1) ON CONFLICT DO NOTHING;
COMMIT;
