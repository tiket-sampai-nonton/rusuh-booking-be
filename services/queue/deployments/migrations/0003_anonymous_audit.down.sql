-- 0003 down: baris dengan hash NULL (entry anonim) tidak bisa dikembalikan ke NOT NULL tanpa kehilangan data.
-- Rollback ditolak bila ada, agar tidak diam-diam menghapus audit.
DO $$
BEGIN
    IF EXISTS (SELECT 1 FROM entry_audit WHERE buyer_nik_hash IS NULL) THEN
        RAISE EXCEPTION 'rollback 0003 ditolak: ada baris entry_audit anonim (buyer_nik_hash NULL)';
    END IF;
END $$;
ALTER TABLE entry_audit ALTER COLUMN buyer_nik_hash SET NOT NULL;
