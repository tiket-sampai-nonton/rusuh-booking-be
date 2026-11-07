-- Reverse of 0010_event_description.up.sql
ALTER TABLE booking.events
    DROP CONSTRAINT IF EXISTS events_description_is_array,
    DROP COLUMN IF EXISTS description;
