-- 0004 down: tabel audit pengaturan belum punya konsumen lain, aman dihapus.
DROP TRIGGER event_settings_audit_no_change ON event_settings_audit;
DROP FUNCTION event_settings_audit_immutable();
DROP TABLE event_settings_audit;
