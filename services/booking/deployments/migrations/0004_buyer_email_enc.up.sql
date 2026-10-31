-- 0004: email pembeli disimpan terenkripsi (UU PDP Pasal 39). Queue mengenkripsi (AES-GCM, AAD booking.buyer_email);
-- Booking hanya menyimpan dan meneruskan ciphertext (base64) ke event; Notification yang membukanya.
-- Kolom plaintext dilonggarkan (NULL) dan tidak lagi ditulis. Baris lama dienkripsi oleh `booking pii-backfill`
-- (idempoten); kolom plaintext baru dihapus di migrasi berikutnya setelah backfill dijalankan di tiap environment.
ALTER TABLE admissions ADD COLUMN IF NOT EXISTS buyer_email_enc text;
ALTER TABLE admissions ALTER COLUMN buyer_email DROP NOT NULL;
ALTER TABLE bookings   ADD COLUMN IF NOT EXISTS buyer_email_enc text;
ALTER TABLE bookings   ALTER COLUMN buyer_email DROP NOT NULL;
COMMENT ON COLUMN admissions.buyer_email_enc IS 'AES-256-GCM base64, AAD booking.buyer_email; hanya Notification yang membuka';
COMMENT ON COLUMN bookings.buyer_email_enc   IS 'AES-256-GCM base64, AAD booking.buyer_email; hanya Notification yang membuka';
