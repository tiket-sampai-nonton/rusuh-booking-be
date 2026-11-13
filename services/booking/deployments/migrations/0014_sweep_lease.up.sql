-- 0014 (AC-C7.3, D-C7.3): sewa (lease) penyapu. Kolom ini status penjadwalan, bukan event: sweeper mengklaim baris dengan
-- UPDATE ... sweep_lease_until selama 15 s, sehingga dua sweeper tidak memproses baris yang sama dan baris yang sedang
-- dalam grace kembali tepat saat grace-nya berakhir.
ALTER TABLE booking.admissions ADD COLUMN sweep_lease_until timestamptz;
ALTER TABLE booking.bookings ADD COLUMN sweep_lease_until timestamptz;
