
-- v4.6: tanpa consumer_inbox; deliveries UNIQUE (source_message_id, notification_type) adalah dedup sekaligus status kirim (§9.8).
CREATE TABLE deliveries (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    source_message_id uuid NOT NULL,
    -- v4.4: TURN_SOON / TURN_ARRIVED dikirim lewat push (mobile); jenis lain lewat email
    notification_type text NOT NULL CHECK (notification_type IN
      ('BOOKING_PAID','BOOKING_EXPIRED','BOOKING_REVIEW','TURN_SOON','TURN_ARRIVED')),
    channel text NOT NULL DEFAULT 'EMAIL' CHECK (channel IN ('EMAIL','PUSH')),
    booking_id uuid,
    queue_entry_id uuid,
    recipient_hash bytea NOT NULL,
    status text NOT NULL CHECK (status IN ('PENDING','SENDING','SENT','RETRY','DEAD')),
    attempts integer NOT NULL DEFAULT 0 CHECK (attempts >= 0),
    provider_message_id text,
    last_error text,
    next_attempt_at timestamptz NOT NULL DEFAULT now(),
    sent_at timestamptz,
    created_at timestamptz NOT NULL DEFAULT now(),
    -- push yang tiba setelah giliran lewat tidak berguna: dibuang, bukan di-retry
    expires_at timestamptz,
    UNIQUE (source_message_id, notification_type),
    CHECK ((booking_id IS NOT NULL) OR (queue_entry_id IS NOT NULL)),
    CHECK ((channel = 'PUSH') = (notification_type IN ('TURN_SOON','TURN_ARRIVED')))
);
CREATE INDEX idx_deliveries_retry ON deliveries(status, next_attempt_at);

-- v4.4: token push perangkat mobile, didaftarkan saat join antrean (izin notifikasi opsional).
CREATE TABLE push_subscriptions (
    visitor_id uuid NOT NULL,
    event_id uuid NOT NULL,
    platform text NOT NULL CHECK (platform IN ('ANDROID','IOS')),
    push_token_enc bytea NOT NULL,          -- dienkripsi; token bukan data publik
    created_at timestamptz NOT NULL DEFAULT now(),
    expires_at timestamptz NOT NULL,        -- dihapus setelah penjualan berakhir
    PRIMARY KEY (visitor_id, event_id)
);

