package telemetry_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/prometheus/client_golang/prometheus/testutil"
	"github.com/stretchr/testify/require"

	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/booking/internal/booking/infrastructure/telemetry"
	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/booking/internal/booking/port/outbound"
)

type fakeFunnel struct {
	admissions, bookings []outbound.FunnelCount
	err                  error
}

func (f fakeFunnel) AdmissionCounts(context.Context) ([]outbound.FunnelCount, error) {
	return f.admissions, f.err
}
func (f fakeFunnel) BookingCounts(context.Context) ([]outbound.FunnelCount, error) {
	return f.bookings, f.err
}

func TestFunnelCollector_ExposesAdmissionsAndBookingsPerEvent(t *testing.T) {
	c := telemetry.NewFunnelCollector(fakeFunnel{
		admissions: []outbound.FunnelCount{
			{EventID: "e1", Status: "ACTIVE", Count: 3},
			{EventID: "e1", Status: "CONVERTED", EndReason: "PAID", Count: 3},
			{EventID: "e1", Status: "EXPIRED", EndReason: "UNCLAIMED", Count: 2},
			{EventID: "e2", Status: "ACTIVE", Count: 1},
		},
		bookings: []outbound.FunnelCount{
			{EventID: "e1", Status: "PAID", Count: 2},
			{EventID: "e1", Status: "EXPIRED", Count: 1},
			{EventID: "e1", Status: "PENDING_PAYMENT", Count: 1},
		},
	})
	want := `
# HELP rbt_booking_admissions Admission (sesi detail) per event, status, dan alasan akhir; dibaca dari PostgreSQL saat scrape.
# TYPE rbt_booking_admissions gauge
rbt_booking_admissions{event_id="e1",reason="",status="ACTIVE"} 3
rbt_booking_admissions{event_id="e1",reason="PAID",status="CONVERTED"} 3
rbt_booking_admissions{event_id="e1",reason="UNCLAIMED",status="EXPIRED"} 2
rbt_booking_admissions{event_id="e2",reason="",status="ACTIVE"} 1
# HELP rbt_booking_bookings Booking per event dan status; dibaca dari PostgreSQL saat scrape.
# TYPE rbt_booking_bookings gauge
rbt_booking_bookings{event_id="e1",status="EXPIRED"} 1
rbt_booking_bookings{event_id="e1",status="PAID"} 2
rbt_booking_bookings{event_id="e1",status="PENDING_PAYMENT"} 1
# HELP rbt_booking_conversion_percent Booking PAID dibanding admission per event (persen).
# TYPE rbt_booking_conversion_percent gauge
rbt_booking_conversion_percent{event_id="e1"} 25
rbt_booking_conversion_percent{event_id="e2"} 0
# HELP rbt_booking_expired_percent Booking EXPIRED dibanding semua booking per event (persen).
# TYPE rbt_booking_expired_percent gauge
rbt_booking_expired_percent{event_id="e1"} 25
# HELP rbt_booking_funnel_up 1 bila metrik funnel terbaca dari PostgreSQL pada scrape ini, 0 bila gagal.
# TYPE rbt_booking_funnel_up gauge
rbt_booking_funnel_up 1
# HELP rbt_booking_missed_turn_percent Admission yang berakhir UNCLAIMED dibanding semua admission per event (persen).
# TYPE rbt_booking_missed_turn_percent gauge
rbt_booking_missed_turn_percent{event_id="e1"} 25
rbt_booking_missed_turn_percent{event_id="e2"} 0
`
	require.NoError(t, testutil.CollectAndCompare(c, strings.NewReader(want)))
}

// A failing read must not break /metrics (HTTP metrics would vanish too): it reports up=0 and no stale numbers.
func TestFunnelCollector_ReadFailureReportsDown(t *testing.T) {
	c := telemetry.NewFunnelCollector(fakeFunnel{err: errors.New("db down")})
	want := `
# HELP rbt_booking_funnel_up 1 bila metrik funnel terbaca dari PostgreSQL pada scrape ini, 0 bila gagal.
# TYPE rbt_booking_funnel_up gauge
rbt_booking_funnel_up 0
`
	require.NoError(t, testutil.CollectAndCompare(c, strings.NewReader(want)))
}
