
CREATE TABLE payment_attempts (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    booking_id uuid NOT NULL,
    attempt_no integer NOT NULL CHECK (attempt_no > 0),
    method text NOT NULL CHECK (method IN ('VA_BCA','VA_BNI','VA_BRI','VA_PERMATA','QRIS','GOPAY')),
    provider text NOT NULL CHECK (provider IN ('MIDTRANS','SIMULATOR')),
    provider_order_id text NOT NULL UNIQUE,
    provider_transaction_id text,
    status text NOT NULL CHECK (status IN
      ('CREATED','PENDING','UNKNOWN','SUCCEEDED','FAILED','CANCELLED','EXPIRED','REFUND_REQUIRED','REFUNDED')),
    amount_minor bigint NOT NULL CHECK (amount_minor >= 0),
    currency char(3) NOT NULL DEFAULT 'IDR',
    expires_at timestamptz NOT NULL,
    instruction jsonb,
    settlement_time timestamptz,
    last_provider_status text,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    UNIQUE (booking_id, attempt_no),
    CHECK ((status NOT IN ('SUCCEEDED','REFUND_REQUIRED','REFUNDED')) OR settlement_time IS NOT NULL)
);
CREATE INDEX idx_attempts_booking ON payment_attempts(booking_id, attempt_no DESC);
CREATE UNIQUE INDEX uq_attempt_provider_tx ON payment_attempts(provider, provider_transaction_id)
    WHERE provider_transaction_id IS NOT NULL;
-- Paling banyak satu attempt yang sedang menunggu pembayaran per booking.
CREATE UNIQUE INDEX uq_attempt_open_per_booking ON payment_attempts(booking_id)
    WHERE status IN ('CREATED','PENDING','UNKNOWN');

CREATE TABLE payment_callbacks (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    provider text NOT NULL,
    fingerprint bytea NOT NULL,
    provider_order_id text NOT NULL,
    normalized_status text NOT NULL,
    signature_valid boolean NOT NULL,
    amount_minor bigint,
    payload_digest bytea NOT NULL,
    received_at timestamptz NOT NULL DEFAULT now(),
    processing_result text NOT NULL DEFAULT 'RECEIVED'
      CHECK (processing_result IN ('RECEIVED','PROCESSED','DUPLICATE','REJECTED','ERROR')),
    UNIQUE (provider, fingerprint)
);
CREATE INDEX idx_callbacks_order ON payment_callbacks(provider_order_id, received_at DESC);

CREATE TABLE payment_page_tokens (
    token_hash bytea PRIMARY KEY,
    booking_id uuid NOT NULL,
    visitor_id uuid NOT NULL,
    expires_at timestamptz NOT NULL,
    used_at timestamptz,
    created_at timestamptz NOT NULL DEFAULT now(),
    CHECK (expires_at > created_at)
);

CREATE TABLE refund_cases (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    attempt_id uuid NOT NULL UNIQUE REFERENCES payment_attempts(id) ON DELETE RESTRICT,
    booking_id uuid NOT NULL,
    reason text NOT NULL CHECK (reason IN ('LATE_SETTLEMENT','SETTLED_AFTER_CANCEL','DUPLICATE_PAYMENT')),
    status text NOT NULL CHECK (status IN ('OPEN','REFUNDED','CLOSED')),
    amount_minor bigint NOT NULL,
    opened_at timestamptz NOT NULL DEFAULT now(),
    resolved_at timestamptz,
    resolved_by text,
    note text
);

CREATE TABLE idempotency_records (
    scope text NOT NULL,
    owner_id uuid NOT NULL,
    idempotency_key text NOT NULL,
    request_hash bytea NOT NULL,
    state text NOT NULL CHECK (state IN ('IN_PROGRESS','COMPLETED')),
    response_status integer,
    response_body jsonb,
    created_at timestamptz NOT NULL DEFAULT now(),
    expires_at timestamptz NOT NULL,
    PRIMARY KEY (scope, owner_id, idempotency_key)
);

CREATE TABLE outbox_events (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    routing_key text NOT NULL,
    aggregate_id uuid NOT NULL,
    schema_version integer NOT NULL DEFAULT 1,
    payload jsonb NOT NULL,
    occurred_at timestamptz NOT NULL DEFAULT now(),
    available_at timestamptz NOT NULL DEFAULT now(),
    published_at timestamptz,
    publish_attempts integer NOT NULL DEFAULT 0,
    last_error text
);
CREATE INDEX idx_payment_outbox_pending ON outbox_events(available_at) WHERE published_at IS NULL;

-- v4.6: tanpa consumer_inbox; booking.review idempoten lewat refund_cases.attempt_id UNIQUE (§9.8).

