-- 0013 (AC-C4.1, D-C4.1): aturan event (max tiket per order/NIK, campuran kategori) terkunci saat jendela penjualan
-- dibuka, bukan saat status menjadi AVAILABLE. Status DRAFT yang sudah lewat jadwal jual sebelumnya tidak terkunci.
-- Mengubah rules setelah jendela dibuka ditolak EVENT_RULES_LOCKED (pemetaan di infrastructure/postgres).

-- Backfill atas nama migrasi, supaya config_audit mencatat asal perubahan.
SELECT set_config('app.actor', 'migration-0013', true);

-- Backfill dijalankan sebelum fungsi guard diganti, selagi guard 0001 masih mengizinkan pembaruan ini.
UPDATE booking.events SET rules_locked_at = sales_starts_at
 WHERE rules_locked_at IS NULL AND sales_starts_at <= now();

CREATE OR REPLACE FUNCTION booking.guard_event_rules() RETURNS trigger LANGUAGE plpgsql AS $fn$
DECLARE
    locked boolean;
BEGIN
    locked := OLD.rules_locked_at IS NOT NULL OR now() >= OLD.sales_starts_at;
    IF locked AND (
         NEW.max_tickets_per_order IS DISTINCT FROM OLD.max_tickets_per_order OR
         NEW.max_tickets_per_buyer_nik IS DISTINCT FROM OLD.max_tickets_per_buyer_nik OR
         NEW.allow_mixed_categories IS DISTINCT FROM OLD.allow_mixed_categories OR
         (OLD.rules_locked_at IS NOT NULL AND NEW.rules_locked_at IS DISTINCT FROM OLD.rules_locked_at)) THEN
        RAISE EXCEPTION 'EVENT_RULES_LOCKED event=%', OLD.id USING ERRCODE = 'check_violation';
    END IF;
    IF locked AND OLD.rules_locked_at IS NULL THEN
        NEW.rules_locked_at := now();
    END IF;
    NEW.updated_at := now();
    RETURN NEW;
END;
$fn$;

CREATE OR REPLACE FUNCTION booking.lock_rules_on_insert() RETURNS trigger LANGUAGE plpgsql AS $fn$
BEGIN
    IF now() >= NEW.sales_starts_at AND NEW.rules_locked_at IS NULL THEN NEW.rules_locked_at := now(); END IF;
    RETURN NEW;
END;
$fn$;
