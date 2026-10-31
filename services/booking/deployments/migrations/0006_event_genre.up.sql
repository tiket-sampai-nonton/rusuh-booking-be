-- 0006: kolom genre (US-015, SRS-F-28 filter genre) dan indeks pendukung katalog.
-- genre bersifat multi-nilai per event (array), dicocokkan dengan overlap (&&); kosong = tidak memfilter.
-- Kolom/indeks ini tidak ada di migrasi sebelumnya (spec baru); file lama tidak diubah sesuai aturan migrasi.
ALTER TABLE booking.events
    ADD COLUMN IF NOT EXISTS genre text[] NOT NULL DEFAULT '{}';

-- GIN mendukung operator overlap (&&) untuk filter genre multi-nilai.
CREATE INDEX IF NOT EXISTS idx_events_genre ON booking.events USING GIN (genre);

-- Btree pada starts_at: membuat filter rentang tanggal (sargable) dan sort DATE_ASC/DATE_DESC dapat memakai indeks.
CREATE INDEX IF NOT EXISTS idx_events_starts_at ON booking.events (starts_at);
