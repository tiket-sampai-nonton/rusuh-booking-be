-- 0005 (PDP-1b): hapus kolom email pembeli plaintext. Sejak PDP-1 Queue mengirim ciphertext (buyer_email_enc) dan
-- kolom ini tidak ditulis lagi. Migrasi menolak berjalan bila masih ada baris plaintext: enkripsi dulu dengan
-- `booking pii-backfill` dari versi sebelum 0005 (go run ./cmd pii-backfill di tag lama), lalu jalankan ulang.
DO $$
BEGIN
    IF EXISTS (SELECT 1 FROM admissions WHERE buyer_email IS NOT NULL AND buyer_email <> '')
       OR EXISTS (SELECT 1 FROM bookings WHERE buyer_email IS NOT NULL AND buyer_email <> '') THEN
        RAISE EXCEPTION 'masih ada email pembeli plaintext; jalankan pii-backfill sebelum migrasi 0005';
    END IF;
END $$;
ALTER TABLE admissions DROP COLUMN buyer_email;
ALTER TABLE bookings   DROP COLUMN buyer_email;
