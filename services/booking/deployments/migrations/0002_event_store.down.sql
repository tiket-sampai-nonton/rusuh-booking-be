DROP INDEX IF EXISTS idx_bookings_account;
ALTER TABLE bookings DROP COLUMN IF EXISTS account_id;
ALTER TABLE admissions DROP COLUMN IF EXISTS account_id;
COMMENT ON COLUMN bookings.version IS NULL;
DROP TABLE IF EXISTS booking_events;
DROP FUNCTION IF EXISTS forbid_event_mutation();
