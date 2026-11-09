//go:build integration

package valkey

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	"github.com/tiket-sampai-nonton/rusuh-booking-be/pkg/clock"

	mock_outbound "github.com/tiket-sampai-nonton/rusuh-booking-be/services/queue/_mock/queue/port/outbound"
	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/queue/internal/queue/application/usecase"
	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/queue/internal/queue/domain/entity"
	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/queue/internal/queue/port/inbound"
	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/queue/internal/queue/port/outbound"
)

const vtokTTL = 24 * time.Hour

func vjoin(t *testing.T, s *QueueStore, ev string, p outbound.JoinParams) outbound.JoinResult {
	t.Helper()
	p.TokenTTL = vtokTTL
	return join(t, s, ev, p)
}

func vtokKey(s *QueueStore, ev, visitor string) string { return s.k(ev, "vtok:"+visitor) }

func pttl(t *testing.T, s *QueueStore, key string) int64 {
	t.Helper()
	n, err := s.c.Do(context.Background(), s.c.B().Pttl().Key(key).Build()).AsInt64()
	require.NoError(t, err)
	return n
}

// TC-QUE-42/43 (P4-P6, real Valkey): a token rotated out by a re-attach is refused (401) on claim, leave and me even
// though the visitor's old entry id is live and ADMITTED; the entry stays ADMITTED and Booking is never called.
func TestVisitorToken_RotatedOutTokenCannotTouchALiveAdmittedEntry(t *testing.T) {
	s := mustStore(t)
	ctx := context.Background()
	ev := evID(t)

	first := vjoin(t, s, ev, outbound.JoinParams{EntryID: "e-rot", NewVisitorID: "dev-a", AccountID: "acct-1"})
	require.Equal(t, outbound.JoinCreated, first.Outcome)
	require.EqualValues(t, 1, first.Gen)
	promoteOne(t, s, ev, "e-rot", "alloc-rot", time.Now().Add(time.Minute).UnixMilli(), time.Now().UnixMilli(), 10)
	require.Equal(t, "ADMITTED", hget(t, s, s.kEntry(ev, "e-rot"), "status"))

	// Same account joins from a new device without the token -> REATTACH rotates the generation.
	re := vjoin(t, s, ev, outbound.JoinParams{NewVisitorID: "dev-b", AccountID: "acct-1"})
	require.Equal(t, outbound.JoinReattach, re.Outcome)
	require.EqualValues(t, 2, re.Gen)

	ctrl := gomock.NewController(t)
	booking := mock_outbound.NewMockIBookingClient(ctrl) // any ReleaseAllocation call fails the test
	tokens := mock_outbound.NewMockITokenIssuer(ctrl)
	for _, entryID := range []string{"e-rot", "someone-elses", "does-not-exist"} {
		_, cerr := usecase.NewClaimTurn(s, tokens, clock.System{}, "https://app/book").Claim(ctx, inbound.ClaimInput{EntryID: entryID, VisitorID: first.VisitorID, Gen: 1, EventID: ev})
		lerr := usecase.NewLeaveQueue(s, booking, clock.System{}).Leave(ctx, inbound.LeaveInput{EntryID: entryID, VisitorID: first.VisitorID, Gen: 1, EventID: ev})
		assert.ErrorIs(t, cerr, entity.ErrVisitorTokenInvalid, entryID)
		assert.ErrorIs(t, lerr, entity.ErrVisitorTokenInvalid, entryID)
	}
	_, merr := usecase.NewGetMyEntry(s).GetMyEntry(ctx, inbound.MyEntryInput{EventID: ev, VisitorID: first.VisitorID, Gen: 1})
	assert.ErrorIs(t, merr, entity.ErrVisitorTokenInvalid)
	assert.Equal(t, "ADMITTED", hget(t, s, s.kEntry(ev, "e-rot"), "status"), "the stale token changed nothing")
}

// P5: with a valid current token, a foreign entry and an absent entry are byte-identical 404s.
func TestVisitorToken_ForeignAndAbsentEntryAreIdentical404(t *testing.T) {
	s := mustStore(t)
	ctx := context.Background()
	ev := evID(t)
	a := vjoin(t, s, ev, outbound.JoinParams{EntryID: "e-a", NewVisitorID: "vis-a"})
	b := vjoin(t, s, ev, outbound.JoinParams{EntryID: "e-b", NewVisitorID: "vis-b"})
	require.Equal(t, outbound.JoinCreated, a.Outcome)
	require.Equal(t, outbound.JoinCreated, b.Outcome)

	ctrl := gomock.NewController(t)
	booking := mock_outbound.NewMockIBookingClient(ctrl)
	tokens := mock_outbound.NewMockITokenIssuer(ctrl)
	claim := usecase.NewClaimTurn(s, tokens, clock.System{}, "https://app/book")
	leave := usecase.NewLeaveQueue(s, booking, clock.System{})

	var msgs []string
	for _, target := range []string{"e-b" /* foreign: owned by vis-b */, "e-absent"} {
		_, cerr := claim.Claim(ctx, inbound.ClaimInput{EntryID: target, VisitorID: "vis-a", Gen: 1, EventID: ev})
		lerr := leave.Leave(ctx, inbound.LeaveInput{EntryID: target, VisitorID: "vis-a", Gen: 1, EventID: ev})
		for _, err := range []error{cerr, lerr} {
			require.ErrorIs(t, err, entity.ErrNotFound)
			ae := inbound.MapError(err)
			msgs = append(msgs, fmt.Sprintf("%d|%s|%s", ae.HTTPStatus, ae.Code, ae.Error()))
		}
	}
	assert.Equal(t, msgs[0], msgs[2])
	assert.Equal(t, msgs[1], msgs[3])
	assert.Equal(t, "WAITING", hget(t, s, s.kEntry(ev, "e-b"), "status"), "the foreign entry is untouched")
}

// P6: a visitor that never joined this event (no vtok record) gets 404 on claim, leave and me; a record for event A
// does not leak into event B.
func TestVisitorToken_NeverJoinedEventIs404(t *testing.T) {
	s := mustStore(t)
	ctx := context.Background()
	evA, evB := evID(t)+"-A", evID(t)+"-B"
	a := vjoin(t, s, evA, outbound.JoinParams{EntryID: "e-a", NewVisitorID: "vis-x"})
	require.Equal(t, outbound.JoinCreated, a.Outcome)

	ctrl := gomock.NewController(t)
	booking := mock_outbound.NewMockIBookingClient(ctrl)
	tokens := mock_outbound.NewMockITokenIssuer(ctrl)
	_, cerr := usecase.NewClaimTurn(s, tokens, clock.System{}, "https://app/book").Claim(ctx, inbound.ClaimInput{EntryID: "e-a", VisitorID: "vis-x", Gen: 1, EventID: evB})
	lerr := usecase.NewLeaveQueue(s, booking, clock.System{}).Leave(ctx, inbound.LeaveInput{EntryID: "e-a", VisitorID: "vis-x", Gen: 1, EventID: evB})
	_, merr := usecase.NewGetMyEntry(s).GetMyEntry(ctx, inbound.MyEntryInput{EventID: evB, VisitorID: "vis-x", Gen: 1})
	assert.ErrorIs(t, cerr, entity.ErrNotFound)
	assert.ErrorIs(t, lerr, entity.ErrNotFound)
	assert.ErrorIs(t, merr, entity.ErrNotFound)
	// ...while event A still answers for the same visitor.
	_, merrA := usecase.NewGetMyEntry(s).GetMyEntry(ctx, inbound.MyEntryInput{EventID: evA, VisitorID: "vis-x", Gen: 1})
	assert.NoError(t, merrA)
}

// P8 (REQ-QVT-03.7): an EXISTING join (token holder, and Idempotency-Key replay) refreshes the vtok TTL to the token
// lifetime, so "record absent" can only mean "never joined" for as long as a token minted by that join is valid.
// EXPIRE never creates a record: a replay whose vtok is gone leaves it gone.
func TestVisitorToken_ExistingJoinRefreshesRecordTTL(t *testing.T) {
	s := mustStore(t)
	ctx := context.Background()
	shrink := func(ev, vis string) {
		require.NoError(t, s.c.Do(ctx, s.c.B().Pexpire().Key(vtokKey(s, ev, vis)).Milliseconds(5_000).Build()).Error())
		require.Less(t, pttl(t, s, vtokKey(s, ev, vis)), int64(6_000))
	}
	assertRefreshed := func(ev, vis string) {
		got := pttl(t, s, vtokKey(s, ev, vis))
		assert.Greater(t, got, vtokTTL.Milliseconds()-60_000, "vtok TTL back to ~TOKEN_VISITOR_TTL")
		assert.LessOrEqual(t, got, vtokTTL.Milliseconds())
	}

	t.Run("token holder EXISTING", func(t *testing.T) {
		ev := evID(t)
		c := vjoin(t, s, ev, outbound.JoinParams{EntryID: "e-1", NewVisitorID: "vis-1"})
		shrink(ev, c.VisitorID)
		r := vjoin(t, s, ev, outbound.JoinParams{TokenVisitorID: c.VisitorID, TokenGen: c.Gen})
		require.Equal(t, outbound.JoinExisting, r.Outcome)
		assertRefreshed(ev, c.VisitorID)
	})
	t.Run("idempotent replay EXISTING", func(t *testing.T) {
		ev := evID(t)
		c := vjoin(t, s, ev, outbound.JoinParams{EntryID: "e-2", NewVisitorID: "vis-2", IdemKey: "idem-0123456789abcdef", IdemTTL: time.Minute})
		shrink(ev, c.VisitorID)
		r := vjoin(t, s, ev, outbound.JoinParams{IdemKey: "idem-0123456789abcdef", IdemTTL: time.Minute})
		require.Equal(t, outbound.JoinExisting, r.Outcome)
		assertRefreshed(ev, c.VisitorID)
	})
	t.Run("account REATTACH keeps the TTL", func(t *testing.T) {
		ev := evID(t)
		c := vjoin(t, s, ev, outbound.JoinParams{EntryID: "e-3", NewVisitorID: "vis-3", AccountID: "acct-3"})
		shrink(ev, c.VisitorID)
		r := vjoin(t, s, ev, outbound.JoinParams{NewVisitorID: "vis-3b", AccountID: "acct-3"})
		require.Equal(t, outbound.JoinReattach, r.Outcome)
		assertRefreshed(ev, c.VisitorID)
	})
	t.Run("EXPIRE on a missing record does not create it", func(t *testing.T) {
		ev := evID(t)
		c := vjoin(t, s, ev, outbound.JoinParams{EntryID: "e-4", NewVisitorID: "vis-4", IdemKey: "idem-fedcba9876543210", IdemTTL: time.Minute})
		require.NoError(t, s.c.Do(ctx, s.c.B().Del().Key(vtokKey(s, ev, c.VisitorID)).Build()).Error())
		r := vjoin(t, s, ev, outbound.JoinParams{IdemKey: "idem-fedcba9876543210", IdemTTL: time.Minute})
		require.Equal(t, outbound.JoinExisting, r.Outcome)
		assert.False(t, keyExists(t, s, vtokKey(s, ev, c.VisitorID)), "no record is resurrected")
	})
}
