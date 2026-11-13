package usecase_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	"github.com/tiket-sampai-nonton/rusuh-booking-be/pkg/clock"

	mock_outbound "github.com/tiket-sampai-nonton/rusuh-booking-be/services/queue/_mock/queue/port/outbound"
	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/queue/internal/queue/application/usecase"
	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/queue/internal/queue/domain/entity"
	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/queue/internal/queue/domain/valueobject"
	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/queue/internal/queue/port/inbound"
	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/queue/internal/queue/port/outbound"
)

type joinMocks struct {
	store    *mock_outbound.MockIQueueStore
	settings *mock_outbound.MockIEventSettingsRepo
	booking  *mock_outbound.MockIBookingClient
	limiter  *mock_outbound.MockIRateLimiter
	tokens   *mock_outbound.MockITokenIssuer
	verifier *mock_outbound.MockIUserTokenVerifier
	audit    *mock_outbound.MockIEntryAuditRepo
}

// joinNow is the fixed clock of every join test.
var joinNow = time.Date(2026, 10, 20, 10, 0, 0, 0, time.UTC)

// openSale is a cached sale window that contains joinNow: the join gate lets the request through.
func openSale() outbound.SaleState {
	return outbound.SaleState{
		SalesStartsMs: joinNow.Add(-time.Hour).UnixMilli(),
		SalesEndsMs:   joinNow.Add(time.Hour).UnixMilli(),
	}
}

// newJoin builds the usecase with an always-open sale window, so tests about the admission path stay focused.
// Gate behaviour (TC-QUE-39/40) uses newJoinRaw and sets its own GetSale expectations.
func newJoin(t *testing.T) (*usecase.JoinQueue, joinMocks) {
	t.Helper()
	uc, m := newJoinRaw(t)
	m.store.EXPECT().GetSale(gomock.Any(), gomock.Any()).Return(openSale(), nil).AnyTimes()
	return uc, m
}

func newJoinRaw(t *testing.T) (*usecase.JoinQueue, joinMocks) {
	t.Helper()
	ctrl := gomock.NewController(t)
	m := joinMocks{
		store:    mock_outbound.NewMockIQueueStore(ctrl),
		settings: mock_outbound.NewMockIEventSettingsRepo(ctrl),
		booking:  mock_outbound.NewMockIBookingClient(ctrl),
		limiter:  mock_outbound.NewMockIRateLimiter(ctrl),
		tokens:   mock_outbound.NewMockITokenIssuer(ctrl),
		verifier: mock_outbound.NewMockIUserTokenVerifier(ctrl),
		audit:    mock_outbound.NewMockIEntryAuditRepo(ctrl),
	}
	uc := usecase.NewJoinQueue(usecase.JoinDeps{
		Store: m.store, Settings: m.settings, Booking: m.booking, Limiter: m.limiter,
		Tokens: m.tokens, UserVerifier: m.verifier, Audit: m.audit,
		Clock: clock.Fixed{T: joinNow},
	}, usecase.JoinSettings{
		JoinPerIP: 60, JoinPerIPWindow: time.Minute,
		ClaimWindow: time.Minute, StatusURLTemplate: "/api/v1/queue-status/",
	})
	return uc, m
}

// created is the join.lua reply for a brand-new entry.
func created(p outbound.JoinParams, seq int64) outbound.JoinResult {
	return outbound.JoinResult{Outcome: outbound.JoinCreated, EntryID: p.EntryID, Seq: seq, VisitorID: p.NewVisitorID, Status: "WAITING"}
}

func TestJoin_AnonymousGuestWaiting(t *testing.T) {
	uc, m := newJoin(t)
	ctx := context.Background()

	m.limiter.EXPECT().Allow(ctx, "join:ip:1.2.3.4", 60, time.Minute).Return(true, nil)
	m.settings.EXPECT().Get(ctx, "ev1").Return(entity.EventSettings{ActiveSlotCapacity: 0, OneActivePerVisitor: true}, true, nil)
	m.store.EXPECT().Join(ctx, gomock.Any()).DoAndReturn(func(_ context.Context, p outbound.JoinParams) (outbound.JoinResult, error) {
		assert.Empty(t, p.AccountID, "guest")
		assert.Empty(t, p.TokenVisitorID)
		assert.NotEmpty(t, p.NewVisitorID)
		return created(p, 7), nil
	})
	m.audit.EXPECT().Append(ctx, gomock.Any()).DoAndReturn(func(_ context.Context, r outbound.AuditRecord) error {
		return nil
	})
	m.tokens.EXPECT().IssueQueue(gomock.Any(), gomock.Any(), gomock.Any()).Return("qt", nil)
	m.tokens.EXPECT().IssueVisitor(gomock.Any(), gomock.Any()).Return("vt", nil)

	resp, err := uc.Join(ctx, inbound.JoinInput{EventID: "ev1", ClientIP: "1.2.3.4"})
	require.NoError(t, err)
	assert.Equal(t, "WAITING", resp.Status)
	assert.Equal(t, int64(7), resp.Seq)
	assert.Equal(t, "qt", resp.QueueToken)
	assert.Equal(t, "vt", resp.VisitorToken)
	assert.False(t, resp.Existing)
	assert.Equal(t, "/api/v1/queue-status/ev1", resp.StatusURL)
}

// P2-14: oneActivePerVisitor=false is never in effect; the join script gets true even when the stored row says false.
func TestJoin_OneActivePerVisitorAlwaysTrueEvenWhenStoredFalse(t *testing.T) {
	uc, m := newJoin(t)
	ctx := context.Background()

	m.limiter.EXPECT().Allow(ctx, "join:ip:1.2.3.4", 60, time.Minute).Return(true, nil)
	m.settings.EXPECT().Get(ctx, "ev1").Return(entity.EventSettings{ActiveSlotCapacity: 0, OneActivePerVisitor: false}, true, nil)
	m.store.EXPECT().Join(ctx, gomock.Any()).DoAndReturn(func(_ context.Context, p outbound.JoinParams) (outbound.JoinResult, error) {
		assert.True(t, p.OneActivePerVisitor)
		return created(p, 7), nil
	})
	m.audit.EXPECT().Append(ctx, gomock.Any()).Return(nil)
	m.tokens.EXPECT().IssueQueue(gomock.Any(), gomock.Any(), gomock.Any()).Return("qt", nil)
	m.tokens.EXPECT().IssueVisitor(gomock.Any(), gomock.Any()).Return("vt", nil)

	_, err := uc.Join(ctx, inbound.JoinInput{EventID: "ev1", ClientIP: "1.2.3.4"})
	require.NoError(t, err)
}

// The bearer identifies the account only: its NIK claims are never read and no NIK/email reaches the store.
func TestJoin_BearerAccountIsPassedAsIdentityOnly(t *testing.T) {
	uc, m := newJoin(t)
	ctx := context.Background()
	m.verifier.EXPECT().Verify("bearer").Return(outbound.UserIdentity{AccountID: "acc-1", HasNIK: true, NIKHash: "ffff", NIKMasked: "3201********0001"}, nil)
	m.limiter.EXPECT().Allow(ctx, gomock.Any(), gomock.Any(), gomock.Any()).Return(true, nil)
	m.settings.EXPECT().Get(ctx, "ev1").Return(entity.EventSettings{}, true, nil)
	m.store.EXPECT().Join(ctx, gomock.Any()).DoAndReturn(func(_ context.Context, p outbound.JoinParams) (outbound.JoinResult, error) {
		assert.Equal(t, "acc-1", p.AccountID)
		assert.Equal(t, "idem-test-acct-01", p.IdemKey)
		return created(p, 1), nil
	})
	m.audit.EXPECT().Append(ctx, gomock.Any()).Return(nil)
	m.tokens.EXPECT().IssueQueue(gomock.Any(), gomock.Any(), gomock.Any()).Return("qt", nil)
	m.tokens.EXPECT().IssueVisitor(gomock.Any(), gomock.Any()).Return("vt", nil)

	_, err := uc.Join(ctx, inbound.JoinInput{EventID: "ev1", Bearer: "bearer", IdempotencyKey: "idem-test-acct-01", ClientIP: "1.2.3.4"})
	require.NoError(t, err)
}

func TestJoin_InvalidBearerIsAGuest(t *testing.T) {
	uc, m := newJoin(t)
	ctx := context.Background()
	m.verifier.EXPECT().Verify("bad").Return(outbound.UserIdentity{}, errors.New("expired"))
	m.limiter.EXPECT().Allow(ctx, gomock.Any(), gomock.Any(), gomock.Any()).Return(true, nil)
	m.settings.EXPECT().Get(ctx, "ev1").Return(entity.EventSettings{}, true, nil)
	m.store.EXPECT().Join(ctx, gomock.Any()).DoAndReturn(func(_ context.Context, p outbound.JoinParams) (outbound.JoinResult, error) {
		assert.Empty(t, p.AccountID)
		return created(p, 1), nil
	})
	m.audit.EXPECT().Append(ctx, gomock.Any()).Return(nil)
	m.tokens.EXPECT().IssueQueue(gomock.Any(), gomock.Any(), gomock.Any()).Return("qt", nil)
	m.tokens.EXPECT().IssueVisitor(gomock.Any(), gomock.Any()).Return("vt", nil)
	_, err := uc.Join(ctx, inbound.JoinInput{EventID: "ev1", Bearer: "bad", ClientIP: "1.2.3.4"})
	require.NoError(t, err)
}

func TestJoin_DirectAdmissionRequestsThreeUnitsWithNoBuyerFields(t *testing.T) {
	uc, m := newJoin(t)
	ctx := context.Background()
	var entryID string

	m.limiter.EXPECT().Allow(ctx, gomock.Any(), gomock.Any(), gomock.Any()).Return(true, nil)
	m.settings.EXPECT().Get(ctx, "ev1").Return(entity.EventSettings{ActiveSlotCapacity: 10}, true, nil)
	m.store.EXPECT().Join(ctx, gomock.Any()).DoAndReturn(func(_ context.Context, p outbound.JoinParams) (outbound.JoinResult, error) {
		entryID = p.EntryID
		return created(p, 1), nil
	})
	m.audit.EXPECT().Append(ctx, gomock.Any()).Return(nil).Times(2) // JOINED + ADMITTED
	m.store.EXPECT().ActiveCount(ctx, "ev1").Return(0, nil).Times(2)
	m.store.EXPECT().TopWaiting(ctx, "ev1", 1).DoAndReturn(func(context.Context, string, int) ([]string, error) { return []string{entryID}, nil }).Times(2)
	m.store.EXPECT().AcquirePromoterLease(ctx, "ev1", gomock.Any()).Return("tok", true, nil)
	m.store.EXPECT().ReleasePromoterLease(gomock.Any(), "ev1", "tok").Return(nil)
	m.booking.EXPECT().ReserveBatch(ctx, gomock.Any()).DoAndReturn(
		func(_ context.Context, req outbound.AllocationBatchRequest) (outbound.AllocationBatchResponse, error) {
			require.Len(t, req.Entries, 1)
			e := req.Entries[0]
			assert.Equal(t, 3, e.Units, "quantity guarantee: 3 units per admission")
			return outbound.AllocationBatchResponse{Allocations: []outbound.AllocationResult{
				{QueueEntryID: entryID, AllocationID: "al1", Status: "GRANTED", ClaimDeadline: "2026-10-20T10:01:00Z"},
			}}, nil
		})
	m.store.EXPECT().Promote(ctx, "ev1", gomock.Any(), 10, gomock.Any(), gomock.Any()).DoAndReturn(
		func(_ context.Context, _ string, c []outbound.PromoteCandidate, _ int, _ int64, _ outbound.PromoteOpts) (outbound.PromoteResult, error) {
			return outbound.PromoteResult{Promoted: []string{c[0].EntryID}}, nil
		})
	m.tokens.EXPECT().IssueQueue(gomock.Any(), gomock.Any(), gomock.Any()).Return("qt", nil)
	m.tokens.EXPECT().IssueVisitor(gomock.Any(), gomock.Any()).Return("vt", nil)

	resp, err := uc.Join(ctx, inbound.JoinInput{EventID: "ev1", ClientIP: "1.2.3.4"})
	require.NoError(t, err)
	assert.Equal(t, "ADMITTED", resp.Status)
}

// An already-active entry (token holder, account re-attach, idempotent replay) is returned as it is now (200) with
// no audit row, no admission attempt and a visitor token only when the caller did not already hold that visitor id.
func TestJoin_ExistingEntryIsReturnedIdempotently(t *testing.T) {
	for _, outcome := range []outbound.JoinOutcome{outbound.JoinExisting, outbound.JoinReattach} {
		t.Run(string(outcome), func(t *testing.T) {
			uc, m := newJoin(t)
			ctx := context.Background()
			m.limiter.EXPECT().Allow(ctx, gomock.Any(), gomock.Any(), gomock.Any()).Return(true, nil)
			m.settings.EXPECT().Get(ctx, "ev1").Return(entity.EventSettings{ActiveSlotCapacity: 10}, true, nil)
			m.store.EXPECT().Join(ctx, gomock.Any()).Return(outbound.JoinResult{Outcome: outcome, EntryID: "e-old", Seq: 4, VisitorID: "v-old", Status: "WAITING", Gen: 3}, nil)
			m.store.EXPECT().GetEntry(ctx, "ev1", "e-old").Return(entity.Entry{ID: "e-old", VisitorID: "v-old", Seq: 4, Status: valueobject.StatusWaiting}, true, nil)
			m.tokens.EXPECT().IssueQueue(gomock.Any(), gomock.Any(), gomock.Any()).Return("qt", nil)
			m.tokens.EXPECT().IssueVisitor(outbound.VisitorClaim{VisitorID: "v-old", Gen: 3}, gomock.Any()).Return("vt", nil) // current generation
			resp, err := uc.Join(ctx, inbound.JoinInput{EventID: "ev1", ClientIP: "1.2.3.4"})
			require.NoError(t, err)
			assert.True(t, resp.Existing)
			assert.Equal(t, "e-old", resp.EntryID)
			assert.Equal(t, int64(4), resp.Seq)
		})
	}
}

func TestJoin_AccountAlreadyInQueueIsConflict(t *testing.T) {
	uc, m := newJoin(t)
	ctx := context.Background()
	m.limiter.EXPECT().Allow(ctx, gomock.Any(), gomock.Any(), gomock.Any()).Return(true, nil)
	m.settings.EXPECT().Get(ctx, "ev1").Return(entity.EventSettings{}, true, nil)
	m.store.EXPECT().Join(ctx, gomock.Any()).Return(outbound.JoinResult{Outcome: outbound.JoinAccountInQueue}, nil)
	_, err := uc.Join(ctx, inbound.JoinInput{EventID: "ev1", ClientIP: "1.2.3.4"})
	require.ErrorIs(t, err, entity.ErrAccountInQueue)
	ae := inbound.MapError(err)
	assert.Equal(t, 409, ae.HTTPStatus)
	assert.Equal(t, "ACCOUNT_ALREADY_IN_QUEUE", string(ae.Code))
}

func TestJoin_RateLimitedIsSoftPerIPOnly(t *testing.T) {
	uc, m := newJoin(t)
	ctx := context.Background()
	m.limiter.EXPECT().Allow(ctx, "join:ip:1.2.3.4", 60, time.Minute).Return(false, nil) // the only limiter call: no per-email
	_, err := uc.Join(ctx, inbound.JoinInput{EventID: "ev1", ClientIP: "1.2.3.4"})
	ae := inbound.MapError(err)
	assert.Equal(t, 429, ae.HTTPStatus)
}

func TestJoin_StorePropagatesError(t *testing.T) {
	uc, m := newJoin(t)
	ctx := context.Background()
	boom := errors.New("valkey down")
	m.limiter.EXPECT().Allow(ctx, gomock.Any(), gomock.Any(), gomock.Any()).Return(true, nil)
	m.settings.EXPECT().Get(ctx, "ev1").Return(entity.EventSettings{}, true, nil)
	m.store.EXPECT().Join(ctx, gomock.Any()).Return(outbound.JoinResult{}, boom)
	_, err := uc.Join(ctx, inbound.JoinInput{EventID: "ev1", ClientIP: "1.2.3.4"})
	assert.ErrorIs(t, err, boom)
}

// TestJoin_UnconfiguredEventIsRefused covers QSA-6 / P10: an event without a settings row is refused with the exact
// sentinel before ANY side effect. gomock fails the test on an unexpected Supply, Store.Join, ReserveBatch, token
// or audit call. An unknown id and a real event without a row take the same path (no disclosure).
func TestJoin_UnconfiguredEventIsRefused(t *testing.T) {
	for _, ev := range []string{"nope", "real-event-without-row"} {
		t.Run(ev, func(t *testing.T) {
			uc, m := newJoin(t)
			ctx := context.Background()
			m.limiter.EXPECT().Allow(ctx, gomock.Any(), gomock.Any(), gomock.Any()).Return(true, nil)
			m.settings.EXPECT().Get(ctx, ev).Return(entity.EventSettings{}, false, nil)
			m.booking.EXPECT().Supply(gomock.Any(), gomock.Any()).Times(0)
			m.booking.EXPECT().ReserveBatch(gomock.Any(), gomock.Any()).Times(0)
			m.store.EXPECT().Join(gomock.Any(), gomock.Any()).Times(0)
			m.tokens.EXPECT().IssueQueue(gomock.Any(), gomock.Any(), gomock.Any()).Times(0)
			m.tokens.EXPECT().IssueVisitor(gomock.Any(), gomock.Any()).Times(0)
			m.audit.EXPECT().Append(gomock.Any(), gomock.Any()).Times(0)

			_, err := uc.Join(ctx, inbound.JoinInput{EventID: ev, ClientIP: "1.2.3.4"})
			require.ErrorIs(t, err, entity.ErrNotConfigured)
			ae := inbound.MapError(err)
			assert.Equal(t, 409, ae.HTTPStatus)
			assert.Equal(t, inbound.CodeQueueNotConfigured, ae.Code)
		})
	}
}

func TestJoin_SettingsReadErrorPropagates(t *testing.T) {
	uc, m := newJoin(t)
	ctx := context.Background()
	boom := errors.New("pg down")
	m.limiter.EXPECT().Allow(ctx, gomock.Any(), gomock.Any(), gomock.Any()).Return(true, nil)
	m.settings.EXPECT().Get(ctx, "ev1").Return(entity.EventSettings{}, false, boom)
	_, err := uc.Join(ctx, inbound.JoinInput{EventID: "ev1", ClientIP: "1.2.3.4"})
	assert.ErrorIs(t, err, boom)
}
