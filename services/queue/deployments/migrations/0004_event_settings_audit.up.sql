-- 0004 (QSA-4): audit perubahan pengaturan antrean per event (SRS-F-26-07). Append-only, ditegakkan di database.
-- Satu baris per field yang berubah; old_value NULL saat pembuatan. Tanpa FK ke event_settings: audit harus tetap
-- ada walau baris pengaturan suatu saat dihapus. actor = subject token admin (id akun), bukan PII.
CREATE TABLE event_settings_audit (
    id         bigserial PRIMARY KEY,
    event_id   uuid        NOT NULL,
    actor      text        NOT NULL,
    field      text        NOT NULL,
    old_value  text,
    new_value  text        NOT NULL,
    changed_at timestamptz NOT NULL
);

CREATE INDEX event_settings_audit_event_idx ON event_settings_audit (event_id, changed_at DESC);

CREATE FUNCTION event_settings_audit_immutable() RETURNS trigger LANGUAGE plpgsql AS
$$
BEGIN
    RAISE EXCEPTION 'event_settings_audit is append-only';
END
$$;

CREATE TRIGGER event_settings_audit_no_change
    BEFORE UPDATE OR DELETE ON event_settings_audit
    FOR EACH ROW EXECUTE FUNCTION event_settings_audit_immutable();
