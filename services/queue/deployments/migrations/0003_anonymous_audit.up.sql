-- 0003 (ADR-030): join anonim. Entry tidak lagi punya NIK pembeli, jadi audit FIFO mencatat NULL untuk entry baru.
-- Baris lama tetap berisi hash; kolom dihapus di rilis expand/contract berikutnya.
ALTER TABLE entry_audit ALTER COLUMN buyer_nik_hash DROP NOT NULL;
