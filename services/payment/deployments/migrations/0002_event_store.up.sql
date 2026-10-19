-- Event sourcing untuk agregat Payment Attempt (ADR-019).
-- payment_events adalah sumber kebenaran siklus attempt; payment_attempts adalah read model
-- yang diproyeksikan secara sinkron di transaksi yang sama dengan append event.

CREATE TABLE payment_events (
    stream_id uuid NOT NULL,                       -- = payment_attempts.id
    version integer NOT NULL CHECK (version > 0),
    event_type text NOT NULL CHECK (event_type ~ '^[A-Z][A-Za-z]+$'),
    schema_version smallint NOT NULL DEFAULT 1 CHECK (schema_version > 0),
    payload jsonb NOT NULL,                        -- tanpa data kartu, server key, atau token
    metadata jsonb NOT NULL DEFAULT '{}'::jsonb,
    occurred_at timestamptz NOT NULL DEFAULT clock_timestamp(),
    global_seq bigint GENERATED ALWAYS AS IDENTITY,
    PRIMARY KEY (stream_id, version)
);
CREATE UNIQUE INDEX uq_payment_events_global_seq ON payment_events(global_seq);
CREATE INDEX idx_payment_events_type_time ON payment_events(event_type, occurred_at);

CREATE FUNCTION forbid_event_mutation() RETURNS trigger
LANGUAGE plpgsql AS $$
BEGIN
    RAISE EXCEPTION 'append-only: % on % is not allowed', TG_OP, TG_TABLE_NAME
        USING ERRCODE = 'restrict_violation';
END $$;
CREATE TRIGGER trg_payment_events_append_only
    BEFORE UPDATE OR DELETE ON payment_events
    FOR EACH ROW EXECUTE FUNCTION forbid_event_mutation();
CREATE TRIGGER trg_payment_events_no_truncate
    BEFORE TRUNCATE ON payment_events
    FOR EACH STATEMENT EXECUTE FUNCTION forbid_event_mutation();

ALTER TABLE payment_attempts ADD COLUMN stream_version integer NOT NULL DEFAULT 0;
COMMENT ON COLUMN payment_attempts.stream_version IS 'Versi stream payment_events terakhir yang diproyeksikan (ADR-019)';
