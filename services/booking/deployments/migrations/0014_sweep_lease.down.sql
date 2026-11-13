-- 0014 down: menghapus kolom lease penyapu. Tidak ada data bisnis yang berubah.
ALTER TABLE booking.bookings DROP COLUMN sweep_lease_until;
ALTER TABLE booking.admissions DROP COLUMN sweep_lease_until;
