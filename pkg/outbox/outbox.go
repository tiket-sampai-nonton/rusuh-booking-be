// Package outbox is the transactional outbox shared by services that publish events (ADR-010).
// Write stores an event in the same transaction as the state change; Relay later publishes pending
// rows to RabbitMQ with publisher confirm, so a broker outage never loses an event (at-least-once).
//
// The table is outbox_events in the service schema (migration 0001), resolved through the connection
// search_path like every other table. messageId of the published envelope is the row id, so a row that
// is published twice (crash before it is marked) carries the same messageId and consumers dedupe on it.
package outbox

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5/pgconn"
)

// Execer is satisfied by pgx.Tx and *pgxpool.Pool. Pass the caller's pgx.Tx to Write so the event
// commits or rolls back together with the state change.
type Execer interface {
	Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error)
}

// Message is one event to publish. RoutingKey is also the envelope eventType.
type Message struct {
	RoutingKey    string
	AggregateID   string // UUID of the aggregate (booking, payment attempt)
	SchemaVersion int    // 0 means 1
	Payload       any    // marshalled to JSON; must follow docs/contracts/events and carry no secrets
}

// Write inserts the message into outbox_events using the caller's transaction.
func Write(ctx context.Context, db Execer, m Message) error {
	if m.RoutingKey == "" || m.AggregateID == "" {
		return errors.New("outbox: routingKey dan aggregateId wajib diisi")
	}
	if m.SchemaVersion == 0 {
		m.SchemaVersion = 1
	}
	raw, err := json.Marshal(m.Payload)
	if err != nil {
		return fmt.Errorf("outbox: marshal payload %s: %w", m.RoutingKey, err)
	}
	if _, err := db.Exec(ctx, `
		INSERT INTO outbox_events (routing_key, aggregate_id, schema_version, payload)
		VALUES ($1, $2::uuid, $3, $4)`, m.RoutingKey, m.AggregateID, m.SchemaVersion, raw); err != nil {
		return fmt.Errorf("outbox: write %s: %w", m.RoutingKey, err)
	}
	return nil
}
