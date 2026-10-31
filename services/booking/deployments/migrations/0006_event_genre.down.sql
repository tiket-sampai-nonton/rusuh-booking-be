-- Reverse of 0006_event_genre.up.sql
DROP INDEX IF EXISTS booking.idx_events_starts_at;
DROP INDEX IF EXISTS booking.idx_events_genre;
ALTER TABLE booking.events
    DROP COLUMN IF EXISTS genre;
