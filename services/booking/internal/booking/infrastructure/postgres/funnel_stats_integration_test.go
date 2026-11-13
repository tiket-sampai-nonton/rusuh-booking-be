//go:build integration

package postgres

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/booking/internal/booking/port/outbound"
)

// NFR-09: the funnel reader counts admissions per event/status/reason straight from the table.
func TestFunnelStats_CountsAdmissionsPerEventAndStatus(t *testing.T) {
	pool := testPool(t)
	ctx := context.Background()
	eventID, _ := seedTestEvent(t, pool, "00000000f0f1", 10)
	seedActiveAdmission(t, pool, "f0000000-0000-0000-0000-000000000001", eventID, "f1000000-0000-0000-0000-000000000001", []byte("h1"))
	seedActiveAdmission(t, pool, "f0000000-0000-0000-0000-000000000002", eventID, "f1000000-0000-0000-0000-000000000002", []byte("h2"))
	_, err := pool.Exec(ctx, `UPDATE booking.admissions SET status='EXPIRED', end_reason='UNCLAIMED', ended_at=now(), outstanding_units=0
		WHERE id='f0000000-0000-0000-0000-000000000002'`)
	require.NoError(t, err)

	got, err := NewFunnelStats(pool).AdmissionCounts(ctx)
	require.NoError(t, err)
	var mine []outbound.FunnelCount
	for _, c := range got {
		if c.EventID == eventID {
			mine = append(mine, c)
		}
	}
	assert.ElementsMatch(t, []outbound.FunnelCount{
		{EventID: eventID, Status: "ACTIVE", Count: 1},
		{EventID: eventID, Status: "EXPIRED", EndReason: "UNCLAIMED", Count: 1},
	}, mine)

	_, err = NewFunnelStats(pool).BookingCounts(ctx)
	require.NoError(t, err)

	names, err := NewFunnelStats(pool).EventNames(ctx)
	require.NoError(t, err)
	assert.Contains(t, names, outbound.EventName{EventID: eventID, Name: "slug-" + eventID},
		"seedTestEvent memakai slug sebagai nama; nama event ikut sebagai label info")
}

// C7.6: the funnel counts only events on sale: a cancelled event, one whose sales have ended and one not yet open
// are left out, while the event on sale now is counted.
func TestFunnelStats_OnlyEventsOnSale(t *testing.T) {
	pool := testPool(t)
	ctx := context.Background()
	// Per-run ids: the rows of an earlier run must not add to this run's counts.
	run := time.Now().UnixNano() % 1e11
	suffix := func(i int) string { return fmt.Sprintf("%011d%1d", run, i) }
	onSale, _ := seedTestEvent(t, pool, suffix(1), 10)
	ended, _ := seedTestEvent(t, pool, suffix(2), 10)
	cancelled, _ := seedTestEvent(t, pool, suffix(3), 10)
	upcoming, _ := seedTestEvent(t, pool, suffix(4), 10)
	_, err := pool.Exec(ctx, `UPDATE booking.events SET sales_ends_at = now() - interval '1 minute' WHERE id = $1::uuid`, ended)
	require.NoError(t, err)
	_, err = pool.Exec(ctx, `UPDATE booking.events SET status = 'CANCELLED' WHERE id = $1::uuid`, cancelled)
	require.NoError(t, err)
	_, err = pool.Exec(ctx, `UPDATE booking.events SET sales_starts_at = now() + interval '1 hour', sales_ends_at = now() + interval '2 hours'
		WHERE id = $1::uuid`, upcoming)
	require.NoError(t, err)

	events := []string{onSale, ended, cancelled, upcoming}
	for i, eventID := range events {
		admID := "f0000000-0000-0000-0000-" + suffix(i+1)
		seedActiveAdmission(t, pool, admID, eventID, "f1000000-0000-0000-0000-"+suffix(i+1), []byte("h"))
		_, err = pool.Exec(ctx, `INSERT INTO booking.bookings (public_ref, event_id, admission_id, visitor_id, buyer_nik_hash, buyer_email_enc, status,
			amount_minor, item_quantity, created_at, payment_expires_at, verification_until)
			VALUES ('RB-' || substr(gen_random_uuid()::text, 1, 8), $1::uuid, $2::uuid, gen_random_uuid(), $3, 'c2VhbGVkLWVtYWls', 'EXPIRED',
			150000, 1, now(), now() + interval '10 minutes', now() + interval '10 minutes')`, eventID, admID, []byte("h"))
		require.NoError(t, err)
	}
	t.Cleanup(func() {
		// End the seeded admissions so the dev DB's session sweeper does not try to release stock never allocated to them.
		_, _ = pool.Exec(context.Background(),
			`UPDATE booking.admissions SET status='EXPIRED', ended_at=now(), end_reason='SESSION_EXPIRED'
			 WHERE event_id::text = ANY($1::text[]) AND status='ACTIVE'`, events)
	})

	admissions, err := NewFunnelStats(pool).AdmissionCounts(ctx)
	require.NoError(t, err)
	bookings, err := NewFunnelStats(pool).BookingCounts(ctx)
	require.NoError(t, err)
	for _, rows := range [][]outbound.FunnelCount{admissions, bookings} {
		for _, r := range rows {
			assert.NotContains(t, []string{ended, cancelled, upcoming}, r.EventID, "rows of an event that is not on sale must be absent")
		}
	}
	assert.Contains(t, admissions, outbound.FunnelCount{EventID: onSale, Status: "ACTIVE", Count: 1})
	assert.Contains(t, bookings, outbound.FunnelCount{EventID: onSale, Status: "EXPIRED", Count: 1})
}
