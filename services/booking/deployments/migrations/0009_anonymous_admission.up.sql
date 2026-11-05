-- 0009 (ADR-030): admission anonim. Reservasi saat giliran tiba tidak lagi membawa NIK/email pembeli;
-- keduanya diikat saat hold di halaman Booking. Perubahan ini juga memindahkan semantik pelepasan
-- allocated_units ke satu penghitung per admission (outstanding_units).
-- CUTOVER: kirim di antara penjualan (tanpa admission RESERVED/ACTIVE/EDITABLE). `migrate up` menolak bila ada,
-- kecuali --allow-live-admissions (cmd/migrate.go).

-- 1. NIK pembeli opsional di admission (diikat saat hold).
ALTER TABLE admissions ALTER COLUMN buyer_nik_hash DROP NOT NULL;

-- 2. Unit milik admission ini yang masih dihitung di event_supply.allocated_units.
ALTER TABLE admissions ADD COLUMN outstanding_units integer NOT NULL DEFAULT 0;
ALTER TABLE admissions ADD CONSTRAINT admissions_outstanding_units_check
    CHECK (outstanding_units >= 0 AND outstanding_units <= units);

-- 3. Penghitung penolakan NIK pembeli per sesi (tanpa PII).
ALTER TABLE admissions ADD COLUMN buyer_nik_refusals integer NOT NULL DEFAULT 0 CHECK (buyer_nik_refusals >= 0);
ALTER TABLE admissions ADD COLUMN buyer_nik_last_refusal_at timestamptz;

-- 4. Batas 3 tiket per NIK dimiliki buyer_quotas; dua admission boleh mengikat NIK pembeli yang sama.
DROP INDEX IF EXISTS uq_admission_active_buyer;

-- 5. Satu admission aktif per akun (backstop dedup Queue).
CREATE UNIQUE INDEX uq_admission_active_account
    ON admissions(event_id, account_id)
    WHERE account_id IS NOT NULL AND status IN ('RESERVED','ACTIVE','EDITABLE');

-- 6. Jaring pengaman: baris RESERVED yang disisipkan tanpa outstanding_units mulai konsisten.
CREATE FUNCTION admissions_default_outstanding() RETURNS trigger LANGUAGE plpgsql AS $fn$
BEGIN
    IF NEW.status = 'RESERVED' AND NEW.outstanding_units = 0 THEN
        NEW.outstanding_units := NEW.units;
    END IF;
    RETURN NEW;
END;
$fn$;
CREATE TRIGGER trg_admissions_default_outstanding BEFORE INSERT ON admissions
    FOR EACH ROW EXECUTE FUNCTION admissions_default_outstanding();

-- 7. Backfill idempoten.
UPDATE admissions SET outstanding_units = units WHERE status IN ('RESERVED','ACTIVE') AND outstanding_units = 0;
UPDATE admissions a
   SET outstanding_units = GREATEST(0, a.units - COALESCE(
        (SELECT b.item_quantity FROM bookings b WHERE b.admission_id = a.id ORDER BY b.created_at DESC LIMIT 1), 0))
 WHERE a.status = 'EDITABLE';

-- 8. Tidak ada penulisan ulang allocated_units (OQ-20): hanya laporan selisih untuk rekonsiliasi manual.
DO $$
DECLARE r record;
BEGIN
    FOR r IN
        SELECT s.event_id, s.allocated_units AS allocated,
               COALESCE((SELECT sum(a.outstanding_units) FROM admissions a
                          WHERE a.event_id = s.event_id AND a.status IN ('RESERVED','ACTIVE','EDITABLE')), 0) AS outstanding
          FROM event_supply s
    LOOP
        IF r.allocated <> r.outstanding THEN
            RAISE NOTICE 'reconcile event=% allocated_units=% sum(outstanding_units)=%', r.event_id, r.allocated, r.outstanding;
        END IF;
    END LOOP;
END $$;
