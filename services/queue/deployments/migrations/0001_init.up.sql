
CREATE TABLE event_settings (
    event_id uuid PRIMARY KEY,
    active_slot_capacity integer NOT NULL DEFAULT 500 CHECK (active_slot_capacity BETWEEN 1 AND 100000),
    near_front_multiplier integer NOT NULL DEFAULT 2 CHECK (near_front_multiplier BETWEEN 1 AND 10),
    status_publish_interval_ms integer NOT NULL DEFAULT 1000 CHECK (status_publish_interval_ms BETWEEN 250 AND 5000),
    -- v4.4: interval polling dikendalikan server lewat Status Document (klien hanya punya cadangan)
    poll_far_ms integer NOT NULL DEFAULT 30000 CHECK (poll_far_ms BETWEEN 5000 AND 60000),
    poll_mid_ms integer NOT NULL DEFAULT 10000 CHECK (poll_mid_ms BETWEEN 2000 AND 30000),
    poll_near_ms integer NOT NULL DEFAULT 3000 CHECK (poll_near_ms BETWEEN 1000 AND 5000),
    one_active_per_visitor boolean NOT NULL DEFAULT true,
    one_active_per_email boolean NOT NULL DEFAULT true,
    category_limited_threshold_pct integer NOT NULL DEFAULT 5 CHECK (category_limited_threshold_pct BETWEEN 1 AND 50),
    pre_turn_notify_enabled boolean NOT NULL DEFAULT true,   -- peringatan "giliran sebentar lagi" + push
    updated_at timestamptz NOT NULL DEFAULT now(),
    CHECK (poll_near_ms <= poll_mid_ms AND poll_mid_ms <= poll_far_ms),
    CHECK (status_publish_interval_ms <= poll_near_ms)
);

-- Append-only, ditulis asinkron (batch) untuk audit FIFO dan pemulihan.
CREATE TABLE entry_audit (
    id bigserial PRIMARY KEY,
    event_id uuid NOT NULL,
    entry_id uuid NOT NULL,
    seq bigint NOT NULL,
    buyer_nik_hash bytea NOT NULL,
    action text NOT NULL CHECK (action IN
      ('JOINED','ADMITTED','CLAIMED','MISSED','LEFT','COMPLETED','ENDED','SOLD_OUT','CLOSED','REJECTED')),
    occurred_at timestamptz NOT NULL,
    metadata jsonb NOT NULL DEFAULT '{}'::jsonb
);
CREATE INDEX idx_entry_audit_event_seq ON entry_audit(event_id, seq);
CREATE INDEX idx_entry_audit_entry ON entry_audit(entry_id, occurred_at);

-- v4.6: tanpa consumer_inbox; consumer idempoten lewat Lua end_entry, SADD, dan HSET (§9.8).

