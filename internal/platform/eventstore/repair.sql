-- Forward-only repair: do not alter the checksum of 000_platform_events.
ALTER TABLE platform_failed_work ALTER COLUMN record_key DROP NOT NULL;
ALTER TABLE platform_failed_work ALTER COLUMN payload DROP NOT NULL;
ALTER TABLE platform_failed_work ADD COLUMN claim_token uuid;
ALTER TABLE platform_failed_work ADD COLUMN claim_until timestamptz;
