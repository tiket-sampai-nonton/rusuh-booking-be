package usecase_test

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"sync"
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

type countingMetrics struct {
	mu sync.Mutex
	n  map[string]int
}

func (c *countingMetrics) Inc(name string, _ ...string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.n == nil {
		c.n = map[string]int{}
	}
	c.n[name]++
}
func (c *countingMetrics) get(name string) int { c.mu.Lock(); defer c.mu.Unlock(); return c.n[name] }

type admissionEnv struct {
	m       joinMocks
	uc      *usecase.JoinQueue
	sleeps  []time.Duration
	metrics *countingMetrics
	logs    *bytes.Buffer
}

// newAdmission builds a JoinQueue with injected Sleep/Jitter (no real waiting) and a captured logger.
func newAdmission(t *testing.T) *admissionEnv {
	t.Helper()
	ctrl := gomock.NewController(t)
	e := &admissionEnv{metrics: &countingMetrics{}, logs: &bytes.Buffer{}}
	e.m = joinMocks{
		store:    mock_outbound.NewMockIQueueStore(ctrl),
		settings: mock_outbound.NewMockIEventSettingsRepo(ctrl),
		booking:  mock_outbound.NewMockIBookingClient(ctrl),
		limiter:  mock_outbound.NewMockIRateLimiter(ctrl),
		tokens:   mock_outbound.NewMockITokenIssuer(ctrl),
		verifier: mock_outbound.NewMockIUserTokenVerifier(ctrl),
		audit:    mock_outbound.NewMockIEntryAuditRepo(ctrl),
	}
	e.uc = usecase.NewJoinQueue(usecase.JoinDeps{
		Store: e.m.store, Settings: e.m.settings, Booking: e.m.booking, Limiter: e.m.limiter,
		Tokens: e.m.tokens, UserVerifier: e.m.verifier, Audit: e.m.audit,
		Clock:  clock.Fixed{T: time.Date(2026, 10, 20, 10, 0, 0, 0, time.UTC)},
		Sleep:  func(_ context.Context, d time.Duration) error { e.sleeps = append(e.sleeps, d); return nil },
		Jitter: func() time.Duration { return 25 * time.Millisecond },
	}, usecase.JoinSettings{
		JoinPerIP: 60, JoinPerIPWindow: time.Minute,
		ClaimWindow: time.Minute, StatusURLTemplate: "/api/v1/queue-status/", AdmissionLeaseTTL: 10 * time.Second,
	}).WithMetrics(e.metrics).WithLogger(slog.New(slog.NewTextHandler(e.logs, nil)))
	return e
}

func (e *admissionEnv) join(ctx context.Context) (string, error) {
	resp, err := e.uc.Join(ctx, inbound.JoinInput{EventID: "ev1", ClientIP: "1.2.3.4"})
	return resp.Status, err
}

type captureID struct{ id string }

// joinCapturing registers a Join expectation that records the generated entry id.
func (e *admissionEnv) joinCapturing(ctx context.Context, c *captureID) {
	e.m.store.EXPECT().Join(ctx, gomock.Any()).DoAndReturn(func(_ context.Context, p outbound.JoinParams) (outbound.JoinResult, error) {
		c.id = p.EntryID
		return outbound.JoinResult{Outcome: outbound.JoinCreated, EntryID: p.EntryID, Seq: 1, VisitorID: p.NewVisitorID, Status: "WAITING"}, nil
	})
}

func (e *admissionEnv) preambleCapturing(ctx context.Context, capacity int, c *captureID) {
	m := e.m
	m.limiter.EXPECT().Allow(ctx, gomock.Any(), gomock.Any(), gomock.Any()).Return(true, nil).AnyTimes()
	m.settings.EXPECT().Get(ctx, "ev1").Return(entity.EventSettings{ActiveSlotCapacity: capacity}, true, nil)
	e.joinCapturing(ctx, c)
	m.audit.EXPECT().Append(ctx, gomock.Any()).Return(nil).AnyTimes()
	m.tokens.EXPECT().IssueQueue(gomock.Any(), gomock.Any(), gomock.Any()).Return("qt", nil)
	m.tokens.EXPECT().IssueVisitor(gomock.Any(), gomock.Any()).Return("vt", nil)
}

func (c *captureID) topWaiting() func(context.Context, string, int) ([]string, error) {
	return func(context.Context, string, int) ([]string, error) { return []string{c.id}, nil }
}

func grantedFor(c *captureID, deadline string) func(context.Context, outbound.AllocationBatchRequest) (outbound.AllocationBatchResponse, error) {
	return func(_ context.Context, _ outbound.AllocationBatchRequest) (outbound.AllocationBatchResponse, error) {
		return outbound.AllocationBatchResponse{Allocations: []outbound.AllocationResult{
			{QueueEntryID: c.id, AllocationID: "al1", Status: "GRANTED", ClaimDeadline: deadline},
		}}, nil
	}
}

// TC-QUE-20: an entry that is not the head must not even reach Booking or take the lease.
func TestJoinAdmission_NotHeadMakesNoBookingCallAndTakesNoLease(t *testing.T) {
	e := newAdmission(t)
	ctx := context.Background()
	var c captureID
	e.preambleCapturing(ctx, 10, &c)
	e.m.store.EXPECT().ActiveCount(ctx, "ev1").Return(0, nil)
	e.m.store.EXPECT().TopWaiting(ctx, "ev1", 1).Return([]string{"someone-earlier"}, nil)
	// no AcquirePromoterLease, no ReserveBatch: gomock fails on any call

	status, err := e.join(ctx)
	require.NoError(t, err)
	assert.Equal(t, "WAITING", status)
}

// TC-QUE-20b: the lease is held for both attempts => WAITING, zero ReserveBatch, two acquires, one sleep.
func TestJoinAdmission_LeaseHeldAcrossBothAttemptsFallsBackToWaiting(t *testing.T) {
	e := newAdmission(t)
	ctx := context.Background()
	var c captureID
	e.preambleCapturing(ctx, 10, &c)
	e.m.store.EXPECT().ActiveCount(ctx, "ev1").Return(0, nil)
	e.m.store.EXPECT().TopWaiting(ctx, "ev1", 1).DoAndReturn(c.topWaiting())
	e.m.store.EXPECT().AcquirePromoterLease(ctx, "ev1", 10*time.Second).Return("", false, nil).Times(2)

	status, err := e.join(ctx)
	require.NoError(t, err)
	assert.Equal(t, "WAITING", status)
	assert.Equal(t, []time.Duration{25 * time.Millisecond}, e.sleeps, "satu kali tidur dengan nilai jitter")
	assert.Equal(t, 1, e.metrics.get(outbound.MetricJoinLeaseRetry))
}

func TestJoinAdmission_LeaseFreedOnTheSecondAttemptAdmitsInTheResponse(t *testing.T) {
	e := newAdmission(t)
	ctx := context.Background()
	var c captureID
	e.preambleCapturing(ctx, 10, &c)
	e.m.store.EXPECT().ActiveCount(ctx, "ev1").Return(0, nil).Times(2) // pre-check + under the lease
	e.m.store.EXPECT().TopWaiting(ctx, "ev1", 1).DoAndReturn(c.topWaiting()).Times(2)
	gomock.InOrder(
		e.m.store.EXPECT().AcquirePromoterLease(ctx, "ev1", 10*time.Second).Return("", false, nil),
		e.m.store.EXPECT().AcquirePromoterLease(ctx, "ev1", 10*time.Second).Return("tok", true, nil),
	)
	e.m.booking.EXPECT().ReserveBatch(ctx, gomock.Any()).DoAndReturn(grantedFor(&c, "2026-10-20T10:01:00Z"))
	e.m.store.EXPECT().Promote(ctx, "ev1", gomock.Any(), 10, gomock.Any(), outbound.PromoteOpts{RequireHead: true, LeaseToken: "tok"}).
		DoAndReturn(func(_ context.Context, _ string, cands []outbound.PromoteCandidate, _ int, _ int64, _ outbound.PromoteOpts) (outbound.PromoteResult, error) {
			return outbound.PromoteResult{Promoted: []string{cands[0].EntryID}}, nil
		})
	e.m.store.EXPECT().ReleasePromoterLease(gomock.Any(), "ev1", "tok").Return(nil)

	status, err := e.join(ctx)
	require.NoError(t, err)
	assert.Equal(t, "ADMITTED", status)
	assert.Len(t, e.sleeps, 1)
}

func TestJoinAdmission_FreeLeaseAdmitsWithoutSleeping(t *testing.T) {
	e := newAdmission(t)
	ctx := context.Background()
	var c captureID
	e.preambleCapturing(ctx, 10, &c)
	e.m.store.EXPECT().ActiveCount(ctx, "ev1").Return(0, nil).Times(2)
	e.m.store.EXPECT().TopWaiting(ctx, "ev1", 1).DoAndReturn(c.topWaiting()).Times(2)
	e.m.store.EXPECT().AcquirePromoterLease(ctx, "ev1", 10*time.Second).Return("tok", true, nil)
	e.m.booking.EXPECT().ReserveBatch(ctx, gomock.Any()).DoAndReturn(grantedFor(&c, "2026-10-20T10:01:00Z"))
	e.m.store.EXPECT().Promote(ctx, "ev1", gomock.Any(), 10, gomock.Any(), gomock.Any()).
		DoAndReturn(func(_ context.Context, _ string, cands []outbound.PromoteCandidate, _ int, _ int64, _ outbound.PromoteOpts) (outbound.PromoteResult, error) {
			return outbound.PromoteResult{Promoted: []string{cands[0].EntryID}}, nil
		})
	e.m.store.EXPECT().ReleasePromoterLease(gomock.Any(), "ev1", "tok").Return(nil)

	status, err := e.join(ctx)
	require.NoError(t, err)
	assert.Equal(t, "ADMITTED", status)
	assert.Empty(t, e.sleeps)
}

// Capacity changed between the lock-free pre-check and the lease: nothing is reserved, the lease is released.
func TestJoinAdmission_RecheckUnderTheLeaseFailsMakesNoBookingCall(t *testing.T) {
	e := newAdmission(t)
	ctx := context.Background()
	var c captureID
	e.preambleCapturing(ctx, 10, &c)
	gomock.InOrder(
		e.m.store.EXPECT().ActiveCount(ctx, "ev1").Return(9, nil),  // pre-check passes
		e.m.store.EXPECT().ActiveCount(ctx, "ev1").Return(10, nil), // under the lease the slot is gone
	)
	e.m.store.EXPECT().TopWaiting(ctx, "ev1", 1).DoAndReturn(c.topWaiting())
	e.m.store.EXPECT().AcquirePromoterLease(ctx, "ev1", 10*time.Second).Return("tok", true, nil)
	e.m.store.EXPECT().ReleasePromoterLease(gomock.Any(), "ev1", "tok").Return(nil)

	status, err := e.join(ctx)
	require.NoError(t, err)
	assert.Equal(t, "WAITING", status)
}

// Promote error / FULL / NOT_HEAD / LeaseLost: WAITING and NEVER a release (the RESERVED row is replayed by the
// promoter); the lease is still released.
func TestJoinAdmission_PromoteFailuresStayWaitingAndNeverRelease(t *testing.T) {
	cases := map[string]struct {
		res outbound.PromoteResult
		err error
	}{
		"error":      {err: errors.New("timeout")},
		"full":       {res: outbound.PromoteResult{Full: true}},
		"not head":   {res: outbound.PromoteResult{Skipped: []outbound.PromoteSkip{{Status: "NOT_HEAD"}}}},
		"lease lost": {res: outbound.PromoteResult{LeaseLost: true}},
		"empty":      {},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			e := newAdmission(t)
			ctx := context.Background()
			var c captureID
			e.preambleCapturing(ctx, 10, &c)
			e.m.store.EXPECT().ActiveCount(ctx, "ev1").Return(0, nil).Times(2)
			e.m.store.EXPECT().TopWaiting(ctx, "ev1", 1).DoAndReturn(c.topWaiting()).Times(2)
			e.m.store.EXPECT().AcquirePromoterLease(ctx, "ev1", 10*time.Second).Return("tok", true, nil)
			e.m.booking.EXPECT().ReserveBatch(ctx, gomock.Any()).DoAndReturn(grantedFor(&c, "2026-10-20T10:01:00Z"))
			e.m.store.EXPECT().Promote(ctx, "ev1", gomock.Any(), 10, gomock.Any(), gomock.Any()).Return(tc.res, tc.err)
			e.m.store.EXPECT().ReleasePromoterLease(gomock.Any(), "ev1", "tok").Return(nil)
			// no ReleaseAllocation expectation: any release fails the test

			status, err := e.join(ctx)
			require.NoError(t, err)
			assert.Equal(t, "WAITING", status)
		})
	}
}

func TestJoinAdmission_LeaseIsReleasedEvenWhenTheRequestContextIsCancelled(t *testing.T) {
	e := newAdmission(t)
	base := context.Background()
	ctx, cancel := context.WithCancel(base)
	var c captureID
	e.preambleCapturing(ctx, 10, &c)
	e.m.store.EXPECT().ActiveCount(ctx, "ev1").Return(0, nil).Times(2)
	e.m.store.EXPECT().TopWaiting(ctx, "ev1", 1).DoAndReturn(c.topWaiting()).Times(2)
	e.m.store.EXPECT().AcquirePromoterLease(ctx, "ev1", 10*time.Second).Return("tok", true, nil)
	e.m.booking.EXPECT().ReserveBatch(ctx, gomock.Any()).DoAndReturn(
		func(_ context.Context, _ outbound.AllocationBatchRequest) (outbound.AllocationBatchResponse, error) {
			cancel() // the caller goes away mid-flight
			return outbound.AllocationBatchResponse{}, context.Canceled
		})
	e.m.store.EXPECT().ReleasePromoterLease(gomock.Any(), "ev1", "tok").DoAndReturn(
		func(rctx context.Context, _, _ string) error {
			assert.NoError(t, rctx.Err(), "pelepasan lease memakai konteks yang tidak ikut dibatalkan")
			return nil
		})

	status, err := e.join(ctx)
	require.NoError(t, err)
	assert.Equal(t, "WAITING", status)
}

// Rule 3a: a replayed GRANTED whose deadline already lapsed is never promoted.
func TestJoinAdmission_LapsedGrantMakesNoPromote(t *testing.T) {
	e := newAdmission(t)
	ctx := context.Background()
	var c captureID
	e.preambleCapturing(ctx, 10, &c)
	e.m.store.EXPECT().ActiveCount(ctx, "ev1").Return(0, nil).Times(2)
	e.m.store.EXPECT().TopWaiting(ctx, "ev1", 1).DoAndReturn(c.topWaiting()).Times(2)
	e.m.store.EXPECT().AcquirePromoterLease(ctx, "ev1", 10*time.Second).Return("tok", true, nil)
	e.m.booking.EXPECT().ReserveBatch(ctx, gomock.Any()).DoAndReturn(grantedFor(&c, "2026-10-20T09:59:00Z")) // before "now"
	e.m.store.EXPECT().ReleasePromoterLease(gomock.Any(), "ev1", "tok").Return(nil)
	// no Promote expectation

	status, err := e.join(ctx)
	require.NoError(t, err)
	assert.Equal(t, "WAITING", status)
}

// A terminal skip (the entry left concurrently) releases the GRANTED allocation exactly once.
func TestJoinAdmission_TerminalSkipReleasesTheGrantedAllocation(t *testing.T) {
	e := newAdmission(t)
	ctx := context.Background()
	var c captureID
	e.preambleCapturing(ctx, 10, &c)
	e.m.store.EXPECT().ActiveCount(ctx, "ev1").Return(0, nil).Times(2)
	e.m.store.EXPECT().TopWaiting(ctx, "ev1", 1).DoAndReturn(c.topWaiting()).Times(2)
	e.m.store.EXPECT().AcquirePromoterLease(ctx, "ev1", 10*time.Second).Return("tok", true, nil)
	e.m.booking.EXPECT().ReserveBatch(ctx, gomock.Any()).DoAndReturn(grantedFor(&c, "2026-10-20T10:01:00Z"))
	e.m.store.EXPECT().Promote(ctx, "ev1", gomock.Any(), 10, gomock.Any(), gomock.Any()).
		DoAndReturn(func(_ context.Context, _ string, cands []outbound.PromoteCandidate, _ int, _ int64, _ outbound.PromoteOpts) (outbound.PromoteResult, error) {
			return outbound.PromoteResult{Skipped: []outbound.PromoteSkip{{EntryID: cands[0].EntryID, Status: "LEFT"}}}, nil
		})
	e.m.booking.EXPECT().ReleaseAllocation(gomock.Any(), "al1", "QUEUE_ENTRY_GONE").Return(nil)
	e.m.store.EXPECT().ReleasePromoterLease(gomock.Any(), "ev1", "tok").Return(nil)

	status, err := e.join(ctx)
	require.NoError(t, err)
	assert.Equal(t, "WAITING", status)
}

// REQ-QSH-13.5: an empty client IP is logged once per join (no PII, no IP value) and the join is not rejected.
func TestJoin_EmptyClientIPIsLoggedAndNotRejected(t *testing.T) {
	e := newAdmission(t)
	ctx := context.Background()
	// no limiter call at all: the per-email limit is gone and an empty IP skips the per-IP limit
	e.m.settings.EXPECT().Get(ctx, "ev1").Return(entity.EventSettings{ActiveSlotCapacity: 0}, true, nil)
	e.m.store.EXPECT().Join(ctx, gomock.Any()).DoAndReturn(func(_ context.Context, p outbound.JoinParams) (outbound.JoinResult, error) {
		return outbound.JoinResult{Outcome: outbound.JoinCreated, EntryID: p.EntryID, Seq: 1, VisitorID: p.NewVisitorID, Status: "WAITING"}, nil
	})
	e.m.audit.EXPECT().Append(ctx, gomock.Any()).Return(nil).AnyTimes()
	e.m.tokens.EXPECT().IssueQueue(gomock.Any(), gomock.Any(), gomock.Any()).Return("qt", nil)
	e.m.tokens.EXPECT().IssueVisitor(gomock.Any(), gomock.Any()).Return("vt", nil)

	_, err := e.uc.Join(ctx, inbound.JoinInput{EventID: "ev1", ClientIP: ""})
	require.NoError(t, err)
	assert.Contains(t, e.logs.String(), "IP klien kosong")
	assert.NotContains(t, e.logs.String(), "a@b.id")
	assert.NotContains(t, e.logs.String(), "3201010101900001")
}
