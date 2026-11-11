-- 0011: description boleh objek terstruktur (longDesc, lineup, location, gallery, terms) selain array Slate lama.
-- Idempotent: aman dijalankan ulang bila constraint sudah dipasang manual di dev.
ALTER TABLE booking.events DROP CONSTRAINT IF EXISTS events_description_is_array;
ALTER TABLE booking.events DROP CONSTRAINT IF EXISTS events_description_is_array_or_object;
ALTER TABLE booking.events
    ADD CONSTRAINT events_description_is_array_or_object
    CHECK (description IS NULL OR jsonb_typeof(description) IN ('array', 'object'));
