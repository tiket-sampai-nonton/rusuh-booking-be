-- Reverse of 0011: objek tidak lolos CHECK array, jadi dikosongkan dulu (deskripsi objek hilang).
UPDATE booking.events SET description = NULL WHERE jsonb_typeof(description) = 'object';
ALTER TABLE booking.events DROP CONSTRAINT IF EXISTS events_description_is_array_or_object;
ALTER TABLE booking.events
    ADD CONSTRAINT events_description_is_array CHECK (description IS NULL OR jsonb_typeof(description) = 'array');
