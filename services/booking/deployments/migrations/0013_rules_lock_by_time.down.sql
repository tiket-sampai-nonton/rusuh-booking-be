-- 0013 down: mengembalikan badan fungsi guard dan insert dari 0001 (kunci berdasarkan status AVAILABLE).
-- rules_locked_at yang sudah diisi backfill 0013 tetap dipertahankan: nilai itu tidak dihapus, hanya kembali
-- diperiksa oleh guard lama (rules_locked_at terisi tetap mengunci).
CREATE OR REPLACE FUNCTION booking.guard_event_rules() RETURNS trigger LANGUAGE plpgsql AS $fn$
BEGIN
    IF NEW.status = 'AVAILABLE' AND OLD.rules_locked_at IS NULL THEN
        NEW.rules_locked_at := now();
    END IF;
    IF OLD.rules_locked_at IS NOT NULL AND (
         NEW.max_tickets_per_order IS DISTINCT FROM OLD.max_tickets_per_order OR
         NEW.max_tickets_per_buyer_nik IS DISTINCT FROM OLD.max_tickets_per_buyer_nik OR
         NEW.allow_mixed_categories IS DISTINCT FROM OLD.allow_mixed_categories OR
         NEW.rules_locked_at IS DISTINCT FROM OLD.rules_locked_at) THEN
        RAISE EXCEPTION 'EVENT_RULES_LOCKED event=%', OLD.id USING ERRCODE = 'check_violation';
    END IF;
    NEW.updated_at := now();
    RETURN NEW;
END;
$fn$;

CREATE OR REPLACE FUNCTION booking.lock_rules_on_insert() RETURNS trigger LANGUAGE plpgsql AS $fn$
BEGIN
    IF NEW.status = 'AVAILABLE' AND NEW.rules_locked_at IS NULL THEN NEW.rules_locked_at := now(); END IF;
    RETURN NEW;
END;
$fn$;
