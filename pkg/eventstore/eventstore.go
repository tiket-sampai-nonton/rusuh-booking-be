// Package eventstore is the small event store kit shared by Booking and Payment (ADR-019).
//
// Event ditulis append-only ke tabel <agregat>_events dengan kunci (stream_id, version). Optimistic
// concurrency: Append dengan expectedVersion yang sudah terlewati ditolak oleh primary key (23505)
// dan dikembalikan sebagai ErrConcurrency. Append selalu dijalankan di dalam transaksi pemanggil,
// bersama perubahan stok relasional, proyeksi read model, dan outbox.
package eventstore

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

// ErrConcurrency means another writer appended to the stream first.
var ErrConcurrency = errors.New("eventstore: stream sudah berubah (version conflict)")

// ErrForbiddenField means the payload carries data that must never enter an append-only log.
var ErrForbiddenField = errors.New("eventstore: payload memuat field terlarang")

var (
	tablePattern = regexp.MustCompile(`^[a-z_][a-z0-9_]*_events$`)
	typePattern  = regexp.MustCompile(`^[A-Z][A-Za-z]+$`)
	// Nama field yang tidak boleh ada di payload event (PII plaintext dan secret). Hash/masked boleh.
	forbidden = map[string]bool{
		"nik": true, "buyernik": true, "attendeenik": true, "holdernik": true,
		"email": true, "buyeremail": true, "password": true, "otp": true,
		"token": true, "accesstoken": true, "refreshtoken": true, "serverkey": true, "cardnumber": true,
	}
)

// Event is a stored event.
type Event struct {
	StreamID      string
	Version       int
	Type          string
	SchemaVersion int
	Payload       json.RawMessage
	Metadata      json.RawMessage
	OccurredAt    time.Time
	GlobalSeq     int64
}

// NewEvent is an event to append.
type NewEvent struct {
	Type          string
	SchemaVersion int // 0 berarti 1
	Payload       any
	Metadata      map[string]string // correlationId, causationId, actor
}

// DB is satisfied by pgx.Tx, *pgx.Conn, and *pgxpool.Pool.
type DB interface {
	Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error)
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
}

// Store reads and writes one event table.
type Store struct {
	table string
}

// New returns a store for table (mis. "booking_events").
func New(table string) (*Store, error) {
	if !tablePattern.MatchString(table) {
		return nil, fmt.Errorf("eventstore: nama tabel %q harus berakhiran _events", table)
	}
	return &Store{table: table}, nil
}

// Append writes events after expectedVersion (0 untuk stream baru) and returns the new version.
func (s *Store) Append(ctx context.Context, db DB, streamID string, expectedVersion int, events ...NewEvent) (int, error) {
	if len(events) == 0 {
		return expectedVersion, nil
	}
	if expectedVersion < 0 {
		return 0, fmt.Errorf("eventstore: expectedVersion %d < 0", expectedVersion)
	}
	version := expectedVersion
	for _, e := range events {
		payload, meta, err := encode(e)
		if err != nil {
			return 0, err
		}
		sv := e.SchemaVersion
		if sv == 0 {
			sv = 1
		}
		version++
		_, err = db.Exec(ctx, fmt.Sprintf(
			`INSERT INTO %s (stream_id, version, event_type, schema_version, payload, metadata) VALUES ($1, $2, $3, $4, $5, $6)`, s.table),
			streamID, version, e.Type, sv, payload, meta)
		if err != nil {
			var pgErr *pgconn.PgError
			if errors.As(err, &pgErr) && pgErr.Code == "23505" {
				return 0, fmt.Errorf("%w: stream %s versi %d", ErrConcurrency, streamID, version)
			}
			return 0, fmt.Errorf("eventstore: append %s v%d: %w", e.Type, version, err)
		}
	}
	return version, nil
}

// Load returns every event of the stream in version order.
func (s *Store) Load(ctx context.Context, db DB, streamID string) ([]Event, error) {
	return s.LoadAfter(ctx, db, streamID, 0)
}

// LoadAfter returns events with version > after (untuk melanjutkan dari snapshot).
func (s *Store) LoadAfter(ctx context.Context, db DB, streamID string, after int) ([]Event, error) {
	rows, err := db.Query(ctx, fmt.Sprintf(
		`SELECT stream_id::text, version, event_type, schema_version, payload, metadata, occurred_at, global_seq
		   FROM %s WHERE stream_id = $1 AND version > $2 ORDER BY version`, s.table), streamID, after)
	if err != nil {
		return nil, fmt.Errorf("eventstore: load %s: %w", streamID, err)
	}
	defer rows.Close()
	var out []Event
	for rows.Next() {
		var e Event
		if err := rows.Scan(&e.StreamID, &e.Version, &e.Type, &e.SchemaVersion, &e.Payload, &e.Metadata, &e.OccurredAt, &e.GlobalSeq); err != nil {
			return nil, fmt.Errorf("eventstore: scan: %w", err)
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

// Applier rebuilds aggregate state from events.
type Applier interface {
	Apply(e Event) error
}

// Replay applies events in order and returns the last version. Versi yang melompat dianggap rusak.
func Replay(events []Event, a Applier) (int, error) {
	version := 0
	for _, e := range events {
		if e.Version != version+1 {
			return version, fmt.Errorf("eventstore: stream %s melompat dari versi %d ke %d", e.StreamID, version, e.Version)
		}
		if err := a.Apply(e); err != nil {
			return version, fmt.Errorf("eventstore: apply %s v%d: %w", e.Type, e.Version, err)
		}
		version = e.Version
	}
	return version, nil
}

func encode(e NewEvent) ([]byte, []byte, error) {
	if !typePattern.MatchString(e.Type) {
		return nil, nil, fmt.Errorf("eventstore: tipe event %q harus PascalCase (mis. BookingHeld)", e.Type)
	}
	payload, err := json.Marshal(e.Payload)
	if err != nil {
		return nil, nil, fmt.Errorf("eventstore: marshal payload %s: %w", e.Type, err)
	}
	if field := findForbidden(payload); field != "" {
		return nil, nil, fmt.Errorf("%w: %q di event %s", ErrForbiddenField, field, e.Type)
	}
	meta := e.Metadata
	if meta == nil {
		meta = map[string]string{}
	}
	metaRaw, err := json.Marshal(meta)
	if err != nil {
		return nil, nil, err
	}
	return payload, metaRaw, nil
}

func findForbidden(raw []byte) string {
	var v any
	if err := json.Unmarshal(raw, &v); err != nil {
		return ""
	}
	return walk(v)
}

func walk(v any) string {
	switch t := v.(type) {
	case map[string]any:
		for k, child := range t {
			if forbidden[strings.ToLower(strings.ReplaceAll(k, "_", ""))] {
				return k
			}
			if f := walk(child); f != "" {
				return f
			}
		}
	case []any:
		for _, child := range t {
			if f := walk(child); f != "" {
				return f
			}
		}
	}
	return ""
}
