-- 0003: kolom city (SRS-F-28-02, TC-CAT-02) dan kolom gambar (SDD-BOOKING App. C.5).
-- Kolom ini tidak ada di 0001 karena spec baru; file lama tidak diubah sesuai aturan migrasi.
ALTER TABLE booking.events
    ADD COLUMN IF NOT EXISTS city          text NOT NULL DEFAULT '',
    ADD COLUMN IF NOT EXISTS image_url     text,
    ADD COLUMN IF NOT EXISTS banner_url    text,
    ADD COLUMN IF NOT EXISTS thumbnail_url text;

-- Indeks untuk filter city case-insensitive (SRS-F-28-02: kota case-insensitive).
CREATE INDEX IF NOT EXISTS idx_events_city ON booking.events (lower(city));
