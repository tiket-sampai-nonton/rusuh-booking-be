//go:build integration

package postgres

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// insertRetentionAdmission inserts an admission in status; endedAt nil means it is still active (the status CHECK
// requires ended_at exactly when the status is terminal).
func insertRetentionAdmission(t *testing.T, f fixture, status string, endedAt *time.Time, reservedAt time.Time, tag string) string {
	t.Helper()
	var endReason *string
	if endedAt != nil {
		r := "SESSION_EXPIRED"
		endReason = &r
	}
	return f.q(t, `
		INSERT INTO booking.admissions (event_id, queue_entry_id, queue_seq, visitor_id, buyer_nik_hash, buyer_email_enc,
			buyer_nik_masked, units, status, cfg_selection_ttl_seconds, cfg_payment_ttl_seconds, cfg_submit_grace_seconds,
			cfg_edit_enabled, cfg_max_edits, cfg_allow_mixed_categories, reserved_at, expires_at, ended_at, end_reason)
		VALUES ($1::uuid, gen_random_uuid(), 1, gen_random_uuid(), $2, 'c2VhbGVkLWVtYWls', '3174********0001',
			1, $3, 300, 600, 5, true, 3, true, $4, $5, $6, $7)
		RETURNING id::text`, f.eventID, []byte("retention-"+tag), status, reservedAt, reservedAt.Add(time.Hour), endedAt, endReason)
}

func insertRetentionChecks(t *testing.T, f fixture, admissionID string, n int) {
	t.Helper()
	for i := 0; i < n; i++ {
		_, err := f.pool.Exec(context.Background(),
			`INSERT INTO booking.attendee_checks (admission_id, attendee_nik_hash) VALUES ($1::uuid, $2)`,
			admissionID, []byte(fmt.Sprintf("check-%s-%d", admissionID, i)))
		require.NoError(t, err)
	}
}

// AC-C7.8 (D-C7.8): each purge removes only the rows past its boundary and keeps rows on the near side of it.
func TestRetention_PurgesOnlyWhatIsDue(t *testing.T) {
	f := seed(t)
	ctx := context.Background()
	repo := NewRetention(f.pool)
	now := time.Now()
	day := 24 * time.Hour

	// Published outbox: 8 days old is due (kept 7 days), 6 days old is kept. An unpublished row is never due.
	outboxDue := f.q(t, `
		INSERT INTO booking.outbox_events (routing_key, aggregate_id, payload, published_at)
		VALUES ('t.due', gen_random_uuid(), '{}', $1) RETURNING id::text`, now.Add(-8*day))
	outboxKept := f.q(t, `
		INSERT INTO booking.outbox_events (routing_key, aggregate_id, payload, published_at)
		VALUES ('t.kept', gen_random_uuid(), '{}', $1) RETURNING id::text`, now.Add(-6*day))
	outboxUnpublished := f.q(t, `
		INSERT INTO booking.outbox_events (routing_key, aggregate_id, payload, available_at, occurred_at)
		VALUES ('t.pending', gen_random_uuid(), '{}', $1, $1) RETURNING id::text`, now.Add(-30*day))

	// Idempotency: expired rows are due in either state; rows that expire later are kept in either state.
	insertIdem := func(key, state string, expiresAt time.Time) {
		_, err := f.pool.Exec(ctx, `
			INSERT INTO booking.idempotency_records (scope, owner_id, idempotency_key, request_hash, state, expires_at)
			VALUES ('retention-test', '33333333-3333-3333-3333-333333333333', $1, $2, $3, $4)`,
			key, []byte(key), state, expiresAt)
		require.NoError(t, err)
	}
	insertIdem("expired-completed", "COMPLETED", now.Add(-time.Minute))
	insertIdem("expired-in-progress", "IN_PROGRESS", now.Add(-time.Minute))
	insertIdem("live-completed", "COMPLETED", now.Add(time.Hour))
	insertIdem("live-in-progress", "IN_PROGRESS", now.Add(time.Hour))

	// Attendee checks: an admission that ended 25 h ago is due; one that ended 23 h ago is kept; an admission that is
	// still ACTIVE or EDITABLE keeps its checks however old it is.
	endedDue := now.Add(-25 * time.Hour)
	endedKept := now.Add(-23 * time.Hour)
	admDue := insertRetentionAdmission(t, f, "EXPIRED", &endedDue, now.Add(-26*time.Hour), "due")
	admKept := insertRetentionAdmission(t, f, "RELEASED", &endedKept, now.Add(-24*time.Hour), "kept")
	admActive := insertRetentionAdmission(t, f, "ACTIVE", nil, now.Add(-48*time.Hour), "active")
	admEditable := insertRetentionAdmission(t, f, "EDITABLE", nil, now.Add(-48*time.Hour), "editable")
	insertRetentionChecks(t, f, admDue, 2)
	insertRetentionChecks(t, f, admKept, 1)
	insertRetentionChecks(t, f, admActive, 1)
	insertRetentionChecks(t, f, admEditable, 1)

	// Outbox: only the published row past 7 days goes.
	n, err := repo.PurgePublishedOutbox(ctx, now.Add(-7*day), 1000)
	require.NoError(t, err)
	assert.Equal(t, 1, n, "one published row is past 7 days")
	assert.Equal(t, 0, f.n(t, `SELECT count(*) FROM booking.outbox_events WHERE id=$1::uuid`, outboxDue))
	assert.Equal(t, 1, f.n(t, `SELECT count(*) FROM booking.outbox_events WHERE id=$1::uuid`, outboxKept))
	assert.Equal(t, 1, f.n(t, `SELECT count(*) FROM booking.outbox_events WHERE id=$1::uuid`, outboxUnpublished))

	// Idempotency: the limit caps one call; a second call takes the rest and no live row goes.
	n, err = repo.PurgeExpiredIdempotency(ctx, now, 1)
	require.NoError(t, err)
	assert.Equal(t, 1, n, "limit 1 deletes one expired record")
	n, err = repo.PurgeExpiredIdempotency(ctx, now, 1000)
	require.NoError(t, err)
	assert.Equal(t, 1, n, "the second expired record goes on the next call")
	assert.Equal(t, 2, f.n(t, `SELECT count(*) FROM booking.idempotency_records WHERE scope='retention-test'`),
		"both live records stay")
	n, err = repo.PurgeExpiredIdempotency(ctx, now, 1000)
	require.NoError(t, err)
	assert.Equal(t, 0, n, "nothing else is expired")

	// Attendee checks: only the checks of the admission that ended before the 24 h boundary go.
	n, err = repo.PurgeAttendeeChecks(ctx, now.Add(-24*time.Hour), 1000)
	require.NoError(t, err)
	assert.Equal(t, 2, n, "two checks of the admission that ended 25 h ago")
	assert.Equal(t, 0, f.n(t, `SELECT count(*) FROM booking.attendee_checks WHERE admission_id=$1::uuid`, admDue))
	assert.Equal(t, 1, f.n(t, `SELECT count(*) FROM booking.attendee_checks WHERE admission_id=$1::uuid`, admKept))
	assert.Equal(t, 1, f.n(t, `SELECT count(*) FROM booking.attendee_checks WHERE admission_id=$1::uuid`, admActive))
	assert.Equal(t, 1, f.n(t, `SELECT count(*) FROM booking.attendee_checks WHERE admission_id=$1::uuid`, admEditable))
}
