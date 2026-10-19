
CREATE TABLE events (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    slug text NOT NULL UNIQUE,
    name text NOT NULL,
    venue text NOT NULL,
    timezone text NOT NULL DEFAULT 'Asia/Jakarta',
    starts_at timestamptz NOT NULL,
    sales_starts_at timestamptz NOT NULL,
    sales_ends_at timestamptz NOT NULL,
    status text NOT NULL CHECK (status IN ('DRAFT','NOT_STARTED','AVAILABLE','SOLD_OUT','CLOSED')),
    max_tickets_per_order integer NOT NULL DEFAULT 3 CHECK (max_tickets_per_order BETWEEN 1 AND 3),
    max_tickets_per_buyer_nik integer NOT NULL DEFAULT 3 CHECK (max_tickets_per_buyer_nik BETWEEN 1 AND 3),
    selection_ttl_seconds integer NOT NULL DEFAULT 300 CHECK (selection_ttl_seconds BETWEEN 60 AND 1800),
    payment_ttl_seconds integer NOT NULL DEFAULT 600 CHECK (payment_ttl_seconds BETWEEN 120 AND 3600),
    claim_window_seconds integer NOT NULL DEFAULT 180 CHECK (claim_window_seconds BETWEEN 60 AND 600),
    max_attendee_checks_per_session integer NOT NULL DEFAULT 10 CHECK (max_attendee_checks_per_session BETWEEN 3 AND 50),
    -- v4.4: aturan pembentuk jaminan (dikunci saat penjualan dibuka)
    allow_mixed_categories boolean NOT NULL DEFAULT true,
    -- v4.4: ubah pesanan sebelum bayar + masa tenggang submit
    selection_edit_enabled boolean NOT NULL DEFAULT true,
    max_selection_edits integer NOT NULL DEFAULT 3 CHECK (max_selection_edits BETWEEN 0 AND 10),
    selection_submit_grace_seconds integer NOT NULL DEFAULT 5 CHECK (selection_submit_grace_seconds BETWEEN 0 AND 30),
    selection_warning_seconds integer NOT NULL DEFAULT 60 CHECK (selection_warning_seconds BETWEEN 0 AND 300),
    rules_locked_at timestamptz,          -- diisi otomatis saat status menjadi AVAILABLE
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    CHECK (sales_ends_at > sales_starts_at),
    -- validasi silang di dalam satu tabel (validasi lintas service: Lampiran J.3)
    CHECK (max_tickets_per_order <= max_tickets_per_buyer_nik),
    CHECK (selection_submit_grace_seconds < selection_ttl_seconds),
    CHECK (selection_warning_seconds < selection_ttl_seconds),
    CHECK (payment_ttl_seconds >= selection_ttl_seconds)
);

-- v4.4: aturan pembentuk jaminan dan keadilan tidak boleh berubah setelah penjualan dibuka.
-- TTL dan jendela boleh berubah; nilai lama tetap berlaku untuk sesi yang sudah berjalan (snapshot di admissions).
CREATE FUNCTION guard_event_rules() RETURNS trigger LANGUAGE plpgsql AS $fn$
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
CREATE TRIGGER trg_guard_event_rules BEFORE UPDATE ON events
    FOR EACH ROW EXECUTE FUNCTION guard_event_rules();
CREATE FUNCTION lock_rules_on_insert() RETURNS trigger LANGUAGE plpgsql AS $fn$
BEGIN
    IF NEW.status = 'AVAILABLE' AND NEW.rules_locked_at IS NULL THEN NEW.rules_locked_at := now(); END IF;
    RETURN NEW;
END;
$fn$;
CREATE TRIGGER trg_lock_rules_on_insert BEFORE INSERT ON events
    FOR EACH ROW EXECUTE FUNCTION lock_rules_on_insert();

-- v4.4: setiap perubahan konfigurasi event tercatat (siapa, kapan, nilai lama → baru).
CREATE TABLE config_audit (
    id bigserial PRIMARY KEY,
    event_id uuid NOT NULL,
    changed_by text NOT NULL DEFAULT current_setting('app.actor', true),
    changed_at timestamptz NOT NULL DEFAULT now(),
    field text NOT NULL,
    old_value jsonb,
    new_value jsonb
);
CREATE FUNCTION audit_event_config() RETURNS trigger LANGUAGE plpgsql AS $fn$
BEGIN
    INSERT INTO booking.config_audit(event_id, changed_by, field, old_value, new_value)
    SELECT NEW.id, COALESCE(current_setting('app.actor', true), 'unknown'), o.key, o.value, n.value
      FROM jsonb_each(to_jsonb(OLD)) o JOIN jsonb_each(to_jsonb(NEW)) n USING (key)
     WHERE o.value IS DISTINCT FROM n.value AND o.key NOT IN ('updated_at');
    RETURN NULL;
END;
$fn$;
CREATE TRIGGER trg_audit_event_config AFTER UPDATE ON events
    FOR EACH ROW EXECUTE FUNCTION audit_event_config();
CREATE INDEX idx_events_status_sales ON events(status, sales_starts_at);

CREATE TABLE ticket_categories (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    event_id uuid NOT NULL REFERENCES events(id) ON DELETE RESTRICT,
    code text NOT NULL,
    name text NOT NULL,
    price_minor bigint NOT NULL CHECK (price_minor >= 0),
    currency char(3) NOT NULL DEFAULT 'IDR',
    display_order integer NOT NULL DEFAULT 0,
    UNIQUE (event_id, code),
    UNIQUE (event_id, id)
);

CREATE TABLE category_inventories (
    category_id uuid PRIMARY KEY,
    event_id uuid NOT NULL,
    capacity integer NOT NULL CHECK (capacity >= 0),
    held_quantity integer NOT NULL DEFAULT 0 CHECK (held_quantity >= 0),
    sold_quantity integer NOT NULL DEFAULT 0 CHECK (sold_quantity >= 0),
    updated_at timestamptz NOT NULL DEFAULT now(),
    FOREIGN KEY (event_id, category_id) REFERENCES ticket_categories(event_id, id) ON DELETE RESTRICT,
    UNIQUE (event_id, category_id),
    CHECK (held_quantity + sold_quantity <= capacity)
);

-- Supply tingkat event: dasar jaminan "masuk pasti dapat tiket".
CREATE TABLE event_supply (
    event_id uuid PRIMARY KEY REFERENCES events(id) ON DELETE RESTRICT,
    total_capacity integer NOT NULL CHECK (total_capacity >= 0),
    allocated_units integer NOT NULL DEFAULT 0 CHECK (allocated_units >= 0),
    held_units integer NOT NULL DEFAULT 0 CHECK (held_units >= 0),
    sold_units integer NOT NULL DEFAULT 0 CHECK (sold_units >= 0),
    version bigint NOT NULL DEFAULT 0,
    updated_at timestamptz NOT NULL DEFAULT now(),
    CHECK (allocated_units + held_units + sold_units <= total_capacity)
);

-- Jaminan "masuk pasti bisa beli" butuh: total stok event = jumlah kapasitas kategori.
-- Diperiksa saat COMMIT (deferred) agar seed kategori + supply bisa dibuat dalam satu transaksi.
CREATE FUNCTION check_supply_matches_categories() RETURNS trigger
LANGUAGE plpgsql AS $fn$
DECLARE
    v_event uuid := COALESCE(NEW.event_id, OLD.event_id);
    v_total integer;
    v_sum integer;
BEGIN
    SELECT total_capacity INTO v_total FROM booking.event_supply WHERE event_id = v_event;
    IF NOT FOUND THEN
        RETURN NULL;  -- event belum punya supply → belum bisa dijual
    END IF;
    SELECT COALESCE(sum(capacity), 0) INTO v_sum FROM booking.category_inventories WHERE event_id = v_event;
    IF v_sum <> v_total THEN
        RAISE EXCEPTION 'SUPPLY_CATEGORY_MISMATCH event=% total=% sum_categories=%', v_event, v_total, v_sum
            USING ERRCODE = 'check_violation';
    END IF;
    RETURN NULL;
END;
$fn$;

CREATE CONSTRAINT TRIGGER trg_supply_matches_categories_supply
    AFTER INSERT OR UPDATE OF total_capacity ON event_supply
    DEFERRABLE INITIALLY DEFERRED FOR EACH ROW EXECUTE FUNCTION check_supply_matches_categories();
CREATE CONSTRAINT TRIGGER trg_supply_matches_categories_category
    AFTER INSERT OR UPDATE OF capacity OR DELETE ON category_inventories
    DEFERRABLE INITIALLY DEFERRED FOR EACH ROW EXECUTE FUNCTION check_supply_matches_categories();

-- Admission = alokasi (reservasi saat giliran tiba) + selection session.
CREATE TABLE admissions (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    event_id uuid NOT NULL REFERENCES events(id) ON DELETE RESTRICT,
    queue_entry_id uuid NOT NULL UNIQUE,
    queue_seq bigint NOT NULL CHECK (queue_seq > 0),
    visitor_id uuid NOT NULL,
    buyer_nik_hash bytea NOT NULL,
    buyer_email text NOT NULL,
    buyer_nik_masked text,                -- mis. 3174********0001, untuk tampilan "NIK pembeli" di halaman beli
    units integer NOT NULL CHECK (units BETWEEN 1 AND 3),
    -- RESERVED: giliran tiba, belum diklaim | ACTIVE: memilih, belum ada hold |
    -- EDITABLE: hold ada, belum menekan "Bayar", TTL detail masih aktif (sisa unit tetap dipegang) |
    -- CONVERTED: pesanan terkunci (lihat lock_reason) | EXPIRED / RELEASED: berakhir tanpa pesanan
    status text NOT NULL CHECK (status IN ('RESERVED','ACTIVE','EDITABLE','CONVERTED','EXPIRED','RELEASED')),
    admission_jti uuid UNIQUE,
    attendee_checks integer NOT NULL DEFAULT 0 CHECK (attendee_checks >= 0),
    edit_count integer NOT NULL DEFAULT 0 CHECK (edit_count >= 0),
    -- snapshot konfigurasi saat giliran diberikan: perubahan konfigurasi tidak memengaruhi sesi berjalan
    cfg_selection_ttl_seconds integer NOT NULL,
    cfg_payment_ttl_seconds integer NOT NULL,
    cfg_submit_grace_seconds integer NOT NULL,
    cfg_edit_enabled boolean NOT NULL,
    cfg_max_edits integer NOT NULL,
    cfg_allow_mixed_categories boolean NOT NULL,
    reserved_at timestamptz NOT NULL DEFAULT now(),
    claimed_at timestamptz,
    expires_at timestamptz NOT NULL,      -- RESERVED: batas klaim; ACTIVE/EDITABLE: batas pilih/ubah (TTL detail)
    locked_at timestamptz,
    lock_reason text CHECK (lock_reason IN ('PAYMENT_STARTED','SELECTION_EXPIRED','EDIT_DISABLED','BOOKING_ENDED')),
    ended_at timestamptz,
    end_reason text CHECK (end_reason IN ('UNCLAIMED','SESSION_EXPIRED','LEFT_WITHOUT_BUYING','CONVERTED')),
    CHECK (expires_at > reserved_at),
    CHECK ((status IN ('RESERVED','ACTIVE','EDITABLE')) = (ended_at IS NULL)),
    CHECK ((status = 'CONVERTED') = (lock_reason IS NOT NULL)),
    CHECK (edit_count <= cfg_max_edits)
);
CREATE INDEX idx_admissions_due ON admissions(status, expires_at) WHERE status IN ('RESERVED','ACTIVE','EDITABLE');
CREATE UNIQUE INDEX uq_admission_active_buyer
    ON admissions(event_id, buyer_nik_hash) WHERE status IN ('RESERVED','ACTIVE','EDITABLE');
-- Anti-calo: satu perangkat (visitor) hanya punya satu giliran aktif per event.
CREATE UNIQUE INDEX uq_admission_active_visitor
    ON admissions(event_id, visitor_id) WHERE status IN ('RESERVED','ACTIVE','EDITABLE');

-- v4.4: NIK pemegang yang sudah dicek dalam satu sesi; cek ulang NIK yang sama tidak dihitung ke batas.
CREATE TABLE attendee_checks (
    admission_id uuid NOT NULL REFERENCES admissions(id) ON DELETE CASCADE,
    attendee_nik_hash bytea NOT NULL,
    checked_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (admission_id, attendee_nik_hash)
);

CREATE TABLE buyer_quotas (
    event_id uuid NOT NULL REFERENCES events(id) ON DELETE RESTRICT,
    buyer_nik_hash bytea NOT NULL,
    held_quantity integer NOT NULL DEFAULT 0 CHECK (held_quantity >= 0),
    paid_quantity integer NOT NULL DEFAULT 0 CHECK (paid_quantity >= 0),
    max_quantity integer NOT NULL DEFAULT 3 CHECK (max_quantity BETWEEN 1 AND 3),
    updated_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (event_id, buyer_nik_hash),
    CHECK (held_quantity + paid_quantity <= max_quantity)
);

CREATE TABLE bookings (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    public_ref text NOT NULL UNIQUE,
    event_id uuid NOT NULL REFERENCES events(id) ON DELETE RESTRICT,
    admission_id uuid NOT NULL UNIQUE REFERENCES admissions(id) ON DELETE RESTRICT,
    visitor_id uuid NOT NULL,
    buyer_nik_hash bytea NOT NULL,
    buyer_email text NOT NULL,
    status text NOT NULL CHECK (status IN ('PENDING_PAYMENT','VERIFYING','PAID','EXPIRED','CANCELLED','REVIEW')),
    currency char(3) NOT NULL DEFAULT 'IDR',
    amount_minor bigint NOT NULL CHECK (amount_minor >= 0),
    item_quantity integer NOT NULL CHECK (item_quantity BETWEEN 1 AND 3),
    created_at timestamptz NOT NULL DEFAULT now(),
    payment_expires_at timestamptz NOT NULL,   -- dihitung sejak hold pertama; tidak di-reset oleh ubah pesanan
    version integer NOT NULL DEFAULT 1,         -- naik setiap ubah pesanan; dikirim ke Payment saat kunci
    payment_started_at timestamptz,             -- v4.4: diisi saat menekan "Bayar" (pesanan terkunci)
    deadline_extended_at timestamptz,
    verification_until timestamptz,
    settlement_time timestamptz,
    paid_at timestamptz,
    ended_at timestamptz,
    updated_at timestamptz NOT NULL DEFAULT now(),
    CHECK (payment_expires_at > created_at),
    CHECK ((status <> 'PAID') OR (paid_at IS NOT NULL AND settlement_time IS NOT NULL)),
    CHECK ((status <> 'VERIFYING') OR verification_until IS NOT NULL)
);
CREATE INDEX idx_bookings_due ON bookings(status, payment_expires_at) WHERE status IN ('PENDING_PAYMENT','VERIFYING');
CREATE INDEX idx_bookings_visitor ON bookings(visitor_id, created_at DESC);

-- Satu baris per tiket (satu pemegang per tiket).
CREATE TABLE booking_items (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    booking_id uuid NOT NULL REFERENCES bookings(id) ON DELETE RESTRICT,
    event_id uuid NOT NULL,
    category_id uuid NOT NULL,
    unit_price_minor bigint NOT NULL CHECK (unit_price_minor >= 0),
    attendee_nik_hash bytea NOT NULL,
    attendee_nik_enc bytea,
    attendee_nik_masked text NOT NULL,
    attendee_name text NOT NULL CHECK (length(attendee_name) BETWEEN 2 AND 100),
    -- REPLACED (v4.4): item lama yang digantikan saat ubah pesanan; disimpan untuk audit
    item_status text NOT NULL CHECK (item_status IN ('HELD','PAID','RELEASED','REPLACED')),
    created_at timestamptz NOT NULL DEFAULT now(),
    FOREIGN KEY (event_id, category_id) REFERENCES category_inventories(event_id, category_id) ON DELETE RESTRICT
);
CREATE UNIQUE INDEX uq_booking_item_nik_active
    ON booking_items(booking_id, attendee_nik_hash) WHERE item_status IN ('HELD','PAID');
CREATE INDEX idx_booking_items_booking ON booking_items(booking_id);
-- "Satu tiket, satu orang": NIK pemegang hanya boleh satu tiket aktif per event.
CREATE UNIQUE INDEX uq_attendee_nik_per_event
    ON booking_items(event_id, attendee_nik_hash) WHERE item_status IN ('HELD','PAID');

CREATE TABLE tickets (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    booking_id uuid NOT NULL REFERENCES bookings(id) ON DELETE RESTRICT,
    booking_item_id uuid NOT NULL UNIQUE REFERENCES booking_items(id) ON DELETE RESTRICT,
    public_code text NOT NULL UNIQUE,
    issued_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX idx_tickets_booking ON tickets(booking_id);

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
CREATE INDEX idx_booking_idem_expiry ON idempotency_records(expires_at);

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
CREATE INDEX idx_booking_outbox_pending ON outbox_events(available_at) WHERE published_at IS NULL;

-- v4.6: tanpa consumer_inbox dan audit_log; consumer idempoten lewat guard state (§9.8).

