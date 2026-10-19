ALTER TABLE payment_attempts DROP COLUMN IF EXISTS stream_version;
DROP TABLE IF EXISTS payment_events;
DROP FUNCTION IF EXISTS forbid_event_mutation();
