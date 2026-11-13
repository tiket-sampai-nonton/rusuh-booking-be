-- 0015 (AC-C7.8, D-C7.8): indeks untuk job retensi. Outbox yang sudah terbit dihapus setelah 7 hari, dan
-- attendee_checks dihapus 1 hari setelah admission berakhir; kedua penghapusan memakai indeks parsial ini agar
-- pemindaian tidak menyentuh baris aktif. Tidak ada data bisnis yang berubah. idx_booking_idem_expiry sudah ada (0001).
-- CUTOVER: pembuatan indeks biasa (tanpa CONCURRENTLY) mengambil kunci SHARE pada booking.outbox_events dan
-- booking.admissions. Kirim di antara penjualan; penulisan ke kedua tabel terblokir selama indeks dibangun.
CREATE INDEX idx_booking_outbox_published ON booking.outbox_events(published_at) WHERE published_at IS NOT NULL;
CREATE INDEX idx_admissions_ended ON booking.admissions(ended_at) WHERE ended_at IS NOT NULL;
