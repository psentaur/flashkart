BEGIN;
SELECT pg_advisory_xact_lock(1);
SET LOCAL ROLE replica_app;
CREATE INDEX IF NOT EXISTS versions_visible_document_c
    ON replica.versions (kind, document_id COLLATE "C") WHERE visible;
DROP INDEX IF EXISTS replica.versions_visible;
INSERT INTO replica.schema_version (version) VALUES (2) ON CONFLICT DO NOTHING;
COMMIT;
