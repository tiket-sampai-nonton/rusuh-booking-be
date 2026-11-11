//go:build integration

package postgres

import (
	"context"
	"testing"

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
