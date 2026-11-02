-- Reverse of 0008_event_cancelled.up.sql: event yang sudah dibatalkan dikembalikan ke CLOSED agar constraint lama berlaku.
UPDATE booking.events SET status = 'CLOSED' WHERE status = 'CANCELLED';
ALTER TABLE booking.events DROP CONSTRAINT IF EXISTS events_status_check;
ALTER TABLE booking.events
    ADD CONSTRAINT events_status_check
    CHECK (status IN ('DRAFT','NOT_STARTED','AVAILABLE','SOLD_OUT','CLOSED'));
