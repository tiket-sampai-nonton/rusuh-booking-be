package postgres

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5"
)

// outboxRow is one outbox message written in the same transaction as the stock or money change.
type outboxRow struct {
	routingKey, aggregateID string
	payload                 any
}

// enqueueOutboxMany writes the rows in one multi-row INSERT, in slice order.
func enqueueOutboxMany(ctx context.Context, tx pgx.Tx, rows []outboxRow) error {
	if len(rows) == 0 {
		return nil
	}
	values := make([]string, 0, len(rows))
	args := make([]any, 0, 3*len(rows))
	for i, r := range rows {
		raw, err := json.Marshal(r.payload)
		if err != nil {
			return err
		}
		values = append(values, fmt.Sprintf("($%d, $%d::uuid, 1, $%d)", 3*i+1, 3*i+2, 3*i+3))
		args = append(args, r.routingKey, r.aggregateID, raw)
	}
	_, err := tx.Exec(ctx, `INSERT INTO booking.outbox_events (routing_key, aggregate_id, schema_version, payload) VALUES `+
		strings.Join(values, ", "), args...)
	return err
}

// reviewRow is booking.review: a payment that needs manual review (late, mismatched, or after the booking ended).
func reviewRow(bookingID, buyerEmailEnc string, amountMinor int64) outboxRow {
	return outboxRow{"booking.review", bookingID,
		map[string]any{"bookingId": bookingID, "buyerEmailEnc": buyerEmailEnc, "amountMinor": amountMinor}}
}

// journeyEndedRow is booking.journey.ended, which tells Queue the buyer's journey finished with outcome
// (PAID | EXPIRED | CANCELLED).
func journeyEndedRow(bookingID, eventID, queueEntryID, outcome string) outboxRow {
	return outboxRow{"booking.journey.ended", bookingID,
		map[string]any{"eventId": eventID, "queueEntryId": queueEntryID, "outcome": outcome}}
}
