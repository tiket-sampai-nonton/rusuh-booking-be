-- Versi pesanan Booking saat payment-lock berhasil; dikirim ke klien sebagai Attempt.bookingVersion.
ALTER TABLE payment_attempts ADD COLUMN booking_version integer NOT NULL DEFAULT 0;
