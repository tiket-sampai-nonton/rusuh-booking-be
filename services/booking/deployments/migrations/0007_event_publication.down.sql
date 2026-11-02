-- Reverse of 0007_event_publication.up.sql
ALTER TABLE booking.events
    DROP COLUMN IF EXISTS published_at;
