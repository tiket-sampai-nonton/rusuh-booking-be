-- Rollback: ciphertext tidak bisa dikembalikan ke plaintext tanpa kunci; baris tanpa plaintext diberi string kosong.
UPDATE admissions SET buyer_email = '' WHERE buyer_email IS NULL;
UPDATE bookings   SET buyer_email = '' WHERE buyer_email IS NULL;
ALTER TABLE admissions ALTER COLUMN buyer_email SET NOT NULL;
ALTER TABLE bookings   ALTER COLUMN buyer_email SET NOT NULL;
ALTER TABLE admissions DROP COLUMN IF EXISTS buyer_email_enc;
ALTER TABLE bookings   DROP COLUMN IF EXISTS buyer_email_enc;
