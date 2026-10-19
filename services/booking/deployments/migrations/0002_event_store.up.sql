-- Event sourcing untuk agregat Booking (ADR-019).
-- booking_events adalah sumber kebenaran siklus booking; tabel bookings adalah read model
-- yang diproyeksikan secara sinkron di transaksi yang sama dengan append event.
-- Stok (category_inventories, event_supply) tetap relasional dan diubah di transaksi yang sama,
-- sehingga invariant "tidak oversell" tidak bergantung pada replay.

CREATE TABLE booking_events (
    stream_id uuid NOT NULL,                       -- = bookings.id
    version integer NOT NULL CHECK (version > 0),  -- urutan per stream; konflik = 23505
    event_type text NOT NULL CHECK (event_type ~ '^[A-Z][A-Za-z]+$'),
    schema_version smallint NOT NULL DEFAULT 1 CHECK (schema_version > 0),
    payload jsonb NOT NULL,                        -- tanpa NIK plaintext, email, atau token
    metadata jsonb NOT NULL DEFAULT '{}'::jsonb,   -- correlationId, causationId, actor
    occurred_at timestamptz NOT NULL DEFAULT clock_timestamp(),
    global_seq bigint GENERATED ALWAYS AS IDENTITY,
    PRIMARY KEY (stream_id, version)
);
CREATE UNIQUE INDEX uq_booking_events_global_seq ON booking_events(global_seq);
CREATE INDEX idx_booking_events_type_time ON booking_events(event_type, occurred_at);

-- Log append-only: UPDATE dan DELETE ditolak di level database.
CREATE FUNCTION forbid_event_mutation() RETURNS trigger
LANGUAGE plpgsql AS $$
BEGIN
    RAISE EXCEPTION 'append-only: % on % is not allowed', TG_OP, TG_TABLE_NAME
        USING ERRCODE = 'restrict_violation';
END $$;
CREATE TRIGGER trg_booking_events_append_only
    BEFORE UPDATE OR DELETE ON booking_events
    FOR EACH ROW EXECUTE FUNCTION forbid_event_mutation();
CREATE TRIGGER trg_booking_events_no_truncate
    BEFORE TRUNCATE ON booking_events
    FOR EACH STATEMENT EXECUTE FUNCTION forbid_event_mutation();

-- Read model: bookings.version kini sama dengan versi event terakhir yang diproyeksikan.
COMMENT ON COLUMN bookings.version IS 'Versi stream booking_events terakhir yang diproyeksikan (ADR-019)';

-- Pembeli yang login (opsional, ADR-020). Guest tetap NULL.
ALTER TABLE admissions ADD COLUMN account_id uuid;
ALTER TABLE bookings ADD COLUMN account_id uuid;
CREATE INDEX idx_bookings_account ON bookings(account_id, created_at DESC) WHERE account_id IS NOT NULL;
