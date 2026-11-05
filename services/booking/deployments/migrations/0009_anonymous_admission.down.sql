-- 0009 down: KEHILANGAN DATA bila ada admission tanpa NIK pembeli atau dua admission aktif dengan NIK yang sama;
-- rollback ditolak bila kondisi itu ada.
DO $$
BEGIN
    IF EXISTS (SELECT 1 FROM admissions WHERE buyer_nik_hash IS NULL) THEN
        RAISE EXCEPTION 'rollback 0009 ditolak: ada admission dengan buyer_nik_hash NULL';
    END IF;
    IF EXISTS (SELECT 1 FROM admissions WHERE status IN ('RESERVED','ACTIVE','EDITABLE')
               GROUP BY event_id, buyer_nik_hash HAVING count(*) > 1) THEN
        RAISE EXCEPTION 'rollback 0009 ditolak: ada admission aktif dengan NIK pembeli yang sama';
    END IF;
END $$;
DROP TRIGGER IF EXISTS trg_admissions_default_outstanding ON admissions;
DROP FUNCTION IF EXISTS admissions_default_outstanding();
DROP INDEX IF EXISTS uq_admission_active_account;
CREATE UNIQUE INDEX uq_admission_active_buyer
    ON admissions(event_id, buyer_nik_hash) WHERE status IN ('RESERVED','ACTIVE','EDITABLE');
ALTER TABLE admissions DROP COLUMN IF EXISTS buyer_nik_last_refusal_at;
ALTER TABLE admissions DROP COLUMN IF EXISTS buyer_nik_refusals;
ALTER TABLE admissions DROP CONSTRAINT IF EXISTS admissions_outstanding_units_check;
ALTER TABLE admissions DROP COLUMN IF EXISTS outstanding_units;
ALTER TABLE admissions ALTER COLUMN buyer_nik_hash SET NOT NULL;
