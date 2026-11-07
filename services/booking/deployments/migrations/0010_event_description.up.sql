-- 0010: deskripsi event dalam format Slate (array node JSON), disimpan sebagai jsonb.
-- NULL = belum ada deskripsi. Bentuk (array objek) dan ukuran divalidasi di usecase; CHECK di sini hanya pengaman.
-- Tidak termasuk aturan terkunci guard_event_rules: deskripsi boleh diubah kapan saja (tidak memengaruhi stok/jaminan).
ALTER TABLE booking.events
    ADD COLUMN IF NOT EXISTS description jsonb,
    ADD CONSTRAINT events_description_is_array CHECK (description IS NULL OR jsonb_typeof(description) = 'array');
