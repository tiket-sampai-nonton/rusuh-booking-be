-- Kolom dikembalikan kosong (nullable); datanya tidak bisa dipulihkan karena memang sudah tidak disimpan.
ALTER TABLE admissions ADD COLUMN buyer_email text;
ALTER TABLE bookings   ADD COLUMN buyer_email text;
