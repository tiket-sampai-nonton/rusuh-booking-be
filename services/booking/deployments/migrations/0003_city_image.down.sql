-- Reverse of 0003_city_image.up.sql
DROP INDEX IF EXISTS booking.idx_events_city;
ALTER TABLE booking.events
    DROP COLUMN IF EXISTS thumbnail_url,
    DROP COLUMN IF EXISTS banner_url,
    DROP COLUMN IF EXISTS image_url,
    DROP COLUMN IF EXISTS city;
