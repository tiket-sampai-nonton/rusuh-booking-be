package bookingclient_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tiket-sampai-nonton/rusuh-booking-be/pkg/resilience"

	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/queue/internal/queue/domain/entity"
	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/queue/internal/queue/infrastructure/bookingclient"
	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/queue/internal/queue/port/outbound"
)

func newBreaker(t *testing.T) *resilience.Breaker {
	t.Helper()
	b, err := resilience.NewBreaker(resilience.BreakerConfig{
		Name: "booking", FailureThreshold: 2, Cooldown: time.Second, HalfOpenProbes: 1, Timeout: time.Second,
	}, time.Now)
	require.NoError(t, err)
	return b
}

func TestReserveBatch_OK(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "/internal/v1/allocations:batch", r.URL.Path)
		assert.Equal(t, http.MethodPost, r.Method)
		// Booking wraps responses in httpx envelope {"meta":{...},"data":{...}}
		_, _ = w.Write([]byte(`{"meta":{"code":200},"data":{"results":[{"queueEntryId":"e1","allocationId":"a1","outcome":"GRANTED","units":1,"claimDeadline":"2026-10-20T10:01:00Z"}]}}`))
	}))
	defer srv.Close()

	c := bookingclient.NewClient(srv.URL, time.Second, newBreaker(t), "")
	resp, err := c.ReserveBatch(context.Background(), outbound.AllocationBatchRequest{
		EventID: "ev1", Entries: []outbound.AllocationEntryRequest{{QueueEntryID: "e1", Units: 1}},
	})
	require.NoError(t, err)
	require.Len(t, resp.Allocations, 1)
	assert.Equal(t, "GRANTED", resp.Allocations[0].Status)
	assert.Equal(t, "a1", resp.Allocations[0].AllocationID)
}

func TestSupply_OK(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "/internal/v1/events/ev1/supply", r.URL.Path)
		// Booking wraps responses in httpx envelope
		_, _ = w.Write([]byte(`{"meta":{"code":200},"data":{"eventId":"ev1","remainingTickets":320,"soldOut":false}}`))
	}))
	defer srv.Close()

	c := bookingclient.NewClient(srv.URL, time.Second, newBreaker(t), "")
	snap, err := c.Supply(context.Background(), "ev1")
	require.NoError(t, err)
	assert.Equal(t, 320, snap.RemainingTickets)
	assert.False(t, snap.SoldOut)
}

func TestReserveBatch_ServerErrorWrappedDependency(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	c := bookingclient.NewClient(srv.URL, time.Second, newBreaker(t), "")
	_, err := c.ReserveBatch(context.Background(), outbound.AllocationBatchRequest{EventID: "ev1"})
	assert.ErrorIs(t, err, entity.ErrDependencyUnavailable)
}

func TestReserveBatch_BreakerOpensAfterFailures(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusBadGateway)
	}))
	defer srv.Close()

	c := bookingclient.NewClient(srv.URL, time.Second, newBreaker(t), "")
	for i := 0; i < 2; i++ {
		_, err := c.Supply(context.Background(), "ev1")
		assert.ErrorIs(t, err, entity.ErrDependencyUnavailable)
	}
	// breaker should now be open — next call fails fast, still mapped to dependency error
	_, err := c.Supply(context.Background(), "ev1")
	assert.ErrorIs(t, err, entity.ErrDependencyUnavailable)
}
