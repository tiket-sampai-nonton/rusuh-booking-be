//go:build integration

package rabbitmq_test

import (
	"context"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tiket-sampai-nonton/rusuh-booking-be/pkg/clock"
	pkgrabbitmq "github.com/tiket-sampai-nonton/rusuh-booking-be/pkg/rabbitmq"
	pkgvalkey "github.com/tiket-sampai-nonton/rusuh-booking-be/pkg/valkey"

	queuerabbitmq "github.com/tiket-sampai-nonton/rusuh-booking-be/services/queue/internal/queue/infrastructure/rabbitmq"
	queuevalkey "github.com/tiket-sampai-nonton/rusuh-booking-be/services/queue/internal/queue/infrastructure/valkey"
	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/queue/internal/queue/port/outbound"
)

// realStore returns a QueueStore on the real Valkey named by TEST_VALKEY_SERVER with a unique key prefix.
// It never skips: an unset variable fails the test (no silent skip).
func realStore(t *testing.T) *queuevalkey.QueueStore {
	t.Helper()
	server := os.Getenv("TEST_VALKEY_SERVER")
	if server == "" {
		t.Fatal("TEST_VALKEY_SERVER wajib diset untuk test integrasi (tanpa skip senyap)")
	}
	prefix := fmt.Sprintf("qje%d", time.Now().UnixNano()%1_000_000_000)
	c, err := pkgvalkey.New(context.Background(), pkgvalkey.Config{Server: server, PrefixEnv: prefix})
	require.NoError(t, err)
	t.Cleanup(c.Close)
	return queuevalkey.NewQueueStore(c.VK(), c, clock.System{}, 16)
}

func joinEntry(t *testing.T, s *queuevalkey.QueueStore, ev string, n int, now int64) string {
	t.Helper()
	id := fmt.Sprintf("entry-%d", n)
	res, err := s.Join(context.Background(), outbound.JoinParams{
		EventID: ev, EntryID: id, NewVisitorID: fmt.Sprintf("visitor-%d", n), AccountID: fmt.Sprintf("acct-%d", n),
		NowMs: now, OneActivePerVisitor: true,
	})
	require.NoError(t, err)
	require.Equal(t, outbound.JoinCreated, res.Outcome)
	return id
}

// promoteWith admits the given entries through promote.lua under a real lease, with the given capacity.
func promoteWith(t *testing.T, s *queuevalkey.QueueStore, ev string, ids []string, capacity int, now int64) outbound.PromoteResult {
	t.Helper()
	ctx := context.Background()
	tok, ok, err := s.AcquirePromoterLease(ctx, ev, 5*time.Second)
	require.NoError(t, err)
	require.True(t, ok)
	defer func() { require.NoError(t, s.ReleasePromoterLease(ctx, ev, tok)) }()
	cands := make([]outbound.PromoteCandidate, 0, len(ids))
	for _, id := range ids {
		cands = append(cands, outbound.PromoteCandidate{EntryID: id, AllocationID: "alloc-" + id, ClaimDeadlineMs: now + 60_000})
	}
	res, err := s.Promote(ctx, ev, cands, capacity, now, outbound.PromoteOpts{LeaseToken: tok})
	require.NoError(t, err)
	return res
}

func journeyEnded(t *testing.T, ev, entry, outcome string) pkgrabbitmq.Delivery {
	t.Helper()
	return delivery(t, "booking.journey.ended", map[string]string{"eventId": ev, "queueEntryId": entry, "outcome": outcome})
}

// TC-QUE-37 (P5, P6, 1.2, 1.6): Booking's journey.ended (outcome LEFT_WITHOUT_BUYING, as emitted by cancel) frees
// exactly one active slot even when delivered twice, and the next WAITING buyer is then admitted.
func TestConsumer_JourneyEnded_LeftWithoutBuying_FreesSlotOnceAndPromotesNext(t *testing.T) {
	s := realStore(t)
	ctx := context.Background()
	ev := fmt.Sprintf("ev-%d", time.Now().UnixNano())
	now := time.Now().UnixMilli()
	c := queuerabbitmq.NewConsumer(s, clock.System{})

	a := joinEntry(t, s, ev, 1, now)
	b := joinEntry(t, s, ev, 2, now)
	require.Contains(t, promoteWith(t, s, ev, []string{a}, 1, now).Promoted, a)

	// Capacity 1 is full: B cannot be promoted while A holds the slot (not vacuous).
	res := promoteWith(t, s, ev, []string{b}, 1, now)
	require.True(t, res.Full)
	require.Empty(t, res.Promoted)
	n, err := s.ActiveCount(ctx, ev)
	require.NoError(t, err)
	require.Equal(t, 1, n)

	d := journeyEnded(t, ev, a, "LEFT_WITHOUT_BUYING")
	require.NoError(t, c.Subscription().Handler(ctx, d))
	n, err = s.ActiveCount(ctx, ev)
	require.NoError(t, err)
	assert.Equal(t, 0, n, "slot freed once")

	// Second delivery (at-least-once): acked, no further change.
	require.NoError(t, c.Subscription().Handler(ctx, d))
	n, err = s.ActiveCount(ctx, ev)
	require.NoError(t, err)
	assert.Equal(t, 0, n, "double send decrements once, never below zero")

	e, found, err := s.GetEntry(ctx, ev, a)
	require.NoError(t, err)
	require.True(t, found)
	assert.Equal(t, "ENDED", string(e.Status))

	// Next buyer is admitted now that a slot is free.
	res = promoteWith(t, s, ev, []string{b}, 1, now)
	assert.Equal(t, []string{b}, res.Promoted)
	eb, found, err := s.GetEntry(ctx, ev, b)
	require.NoError(t, err)
	require.True(t, found)
	assert.Equal(t, "ADMITTED", string(eb.Status))
}

// TC-QUE-37: a broken payload is a permanent failure (DLQ), never retried.
func TestConsumer_JourneyEnded_BrokenPayloadIsPermanent(t *testing.T) {
	s := realStore(t)
	c := queuerabbitmq.NewConsumer(s, clock.System{})
	err := c.Subscription().Handler(context.Background(), delivery(t, "booking.journey.ended", map[string]string{"eventId": "ev"}))
	require.Error(t, err)
	assert.True(t, pkgrabbitmq.IsPermanent(err))
}
