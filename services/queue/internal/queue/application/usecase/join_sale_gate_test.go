package usecase_test

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/queue/internal/queue/domain/entity"
	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/queue/internal/queue/port/inbound"
	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/queue/internal/queue/port/outbound"
)

// expectAccepted wires the happy path after the gate: store join, audit and tokens.
func expectAccepted(m joinMocks) {
	m.store.EXPECT().Join(gomock.Any(), gomock.Any()).DoAndReturn(func(_ context.Context, p outbound.JoinParams) (outbound.JoinResult, error) {
		return created(p, 1), nil
	})
	m.audit.EXPECT().Append(gomock.Any(), gomock.Any()).Return(nil)
	m.tokens.EXPECT().IssueQueue(gomock.Any(), gomock.Any(), gomock.Any()).Return("qt", nil)
	m.tokens.EXPECT().IssueVisitor(gomock.Any(), gomock.Any()).Return("vt", nil)
}

func expectLimiterAndSettings(m joinMocks, times int) {
	m.limiter.EXPECT().Allow(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).Return(true, nil).Times(times)
	m.settings.EXPECT().Get(gomock.Any(), "ev1").Return(entity.EventSettings{}, true, nil).Times(times)
}

func ms(t time.Time) int64 { return t.UnixMilli() }

// TC-QUE-39 / TC-QUE-40 (P11, P12): the join gate. A rejected join creates no entry, consumes no seq and,
// when the window is cached, makes no Booking call. Precedence: not started 409 > sold out 410 > ended 409.
func TestJoin_SaleGate(t *testing.T) {
	const ms1 = time.Millisecond
	tests := []struct {
		name    string
		sale    outbound.SaleState
		wantErr error // nil = accepted
	}{
		{"before start by 1ms", outbound.SaleState{SalesStartsMs: ms(joinNow.Add(ms1)), SalesEndsMs: ms(joinNow.Add(time.Hour))}, entity.ErrNotOnSale},
		{"exactly at start is open", outbound.SaleState{SalesStartsMs: ms(joinNow), SalesEndsMs: ms(joinNow.Add(time.Hour))}, nil},
		{"1ms before end is open", outbound.SaleState{SalesStartsMs: ms(joinNow.Add(-time.Hour)), SalesEndsMs: ms(joinNow.Add(ms1))}, nil},
		{"exactly at end is closed", outbound.SaleState{SalesStartsMs: ms(joinNow.Add(-time.Hour)), SalesEndsMs: ms(joinNow)}, entity.ErrNotOnSale},
		{"sold out inside the window", outbound.SaleState{SaleState: "CLOSED", SoldOut: true, SalesStartsMs: ms(joinNow.Add(-time.Hour)), SalesEndsMs: ms(joinNow.Add(time.Hour))}, entity.ErrSoldOut},
		{"not started beats sold out", outbound.SaleState{SaleState: "CLOSED", SoldOut: true, SalesStartsMs: ms(joinNow.Add(time.Hour)), SalesEndsMs: ms(joinNow.Add(2 * time.Hour))}, entity.ErrNotOnSale},
		{"sold out beats ended", outbound.SaleState{SaleState: "CLOSED", SoldOut: true, SalesStartsMs: ms(joinNow.Add(-2 * time.Hour)), SalesEndsMs: ms(joinNow.Add(-time.Hour))}, entity.ErrSoldOut},
		{"ended and not sold out", outbound.SaleState{SalesStartsMs: ms(joinNow.Add(-2 * time.Hour)), SalesEndsMs: ms(joinNow.Add(-time.Hour))}, entity.ErrNotOnSale},
		{"CLOSED without sold-out flag is not sold out", outbound.SaleState{SaleState: "CLOSED", SalesStartsMs: ms(joinNow.Add(-time.Hour)), SalesEndsMs: ms(joinNow.Add(time.Hour))}, nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			uc, m := newJoinRaw(t)
			expectLimiterAndSettings(m, 1)
			m.store.EXPECT().GetSale(gomock.Any(), "ev1").Return(tt.sale, nil)
			if tt.wantErr == nil {
				expectAccepted(m)
			} // else: Store.Join / Booking.Supply have no expectation, so any call fails the test (Times(0))
			_, err := uc.Join(context.Background(), inbound.JoinInput{EventID: "ev1", ClientIP: "1.2.3.4"})
			if tt.wantErr != nil {
				assert.ErrorIs(t, err, tt.wantErr)
				return
			}
			require.NoError(t, err)
		})
	}
}

// TC-QUE-40 case 3: a replay with the same Idempotency-Key after sold out is refused too (documented trade-off).
func TestJoin_SaleGate_RepeatedAndReplayRejectionsAreIdentical(t *testing.T) {
	uc, m := newJoinRaw(t)
	expectLimiterAndSettings(m, 2)
	soldOut := outbound.SaleState{SaleState: "CLOSED", SoldOut: true, SalesStartsMs: ms(joinNow.Add(-time.Hour)), SalesEndsMs: ms(joinNow.Add(time.Hour))}
	m.store.EXPECT().GetSale(gomock.Any(), "ev1").Return(soldOut, nil).Times(2)
	in := inbound.JoinInput{EventID: "ev1", ClientIP: "1.2.3.4", IdempotencyKey: "idem-test-dup-01"}
	_, e1 := uc.Join(context.Background(), in)
	_, e2 := uc.Join(context.Background(), in)
	require.ErrorIs(t, e1, entity.ErrSoldOut)
	assert.Equal(t, e1, e2, "rejection is a pure function of clock and sale state")
}

// TC-QUE-39 (P13, 4.5): unknown window -> exactly one Booking read, cached, then decided; Booking down -> 503.
func TestJoin_SaleGate_UnknownWindow(t *testing.T) {
	startsAt, endsAt := joinNow.Add(-time.Hour), joinNow.Add(time.Hour)
	t.Run("Supply fills and caches the window then accepts", func(t *testing.T) {
		uc, m := newJoinRaw(t)
		expectLimiterAndSettings(m, 1)
		m.store.EXPECT().GetSale(gomock.Any(), "ev1").Return(outbound.SaleState{}, nil)
		m.booking.EXPECT().Supply(gomock.Any(), "ev1").Return(outbound.SupplySnapshot{SalesStartsAt: startsAt, SalesEndsAt: endsAt}, nil).Times(1)
		m.store.EXPECT().SetSaleWindow(gomock.Any(), "ev1", ms(startsAt), ms(endsAt)).Return(nil)
		expectAccepted(m)
		_, err := uc.Join(context.Background(), inbound.JoinInput{EventID: "ev1", ClientIP: "1.2.3.4"})
		require.NoError(t, err)
	})
	t.Run("Supply says not started: cached then refused", func(t *testing.T) {
		uc, m := newJoinRaw(t)
		expectLimiterAndSettings(m, 1)
		m.store.EXPECT().GetSale(gomock.Any(), "ev1").Return(outbound.SaleState{}, nil)
		later := joinNow.Add(time.Hour)
		m.booking.EXPECT().Supply(gomock.Any(), "ev1").Return(outbound.SupplySnapshot{SalesStartsAt: later, SalesEndsAt: later.Add(time.Hour)}, nil)
		m.store.EXPECT().SetSaleWindow(gomock.Any(), "ev1", gomock.Any(), gomock.Any()).Return(nil)
		_, err := uc.Join(context.Background(), inbound.JoinInput{EventID: "ev1", ClientIP: "1.2.3.4"})
		assert.ErrorIs(t, err, entity.ErrNotOnSale)
	})
	t.Run("a failed cache write does not fail the join", func(t *testing.T) {
		uc, m := newJoinRaw(t)
		expectLimiterAndSettings(m, 1)
		m.store.EXPECT().GetSale(gomock.Any(), "ev1").Return(outbound.SaleState{}, nil)
		m.booking.EXPECT().Supply(gomock.Any(), "ev1").Return(outbound.SupplySnapshot{SalesStartsAt: startsAt, SalesEndsAt: endsAt}, nil)
		m.store.EXPECT().SetSaleWindow(gomock.Any(), "ev1", gomock.Any(), gomock.Any()).Return(errors.New("valkey down"))
		expectAccepted(m)
		_, err := uc.Join(context.Background(), inbound.JoinInput{EventID: "ev1", ClientIP: "1.2.3.4"})
		require.NoError(t, err)
	})
	t.Run("Booking unavailable fails closed with 503, no entry", func(t *testing.T) {
		uc, m := newJoinRaw(t)
		expectLimiterAndSettings(m, 1)
		m.store.EXPECT().GetSale(gomock.Any(), "ev1").Return(outbound.SaleState{}, nil)
		m.booking.EXPECT().Supply(gomock.Any(), "ev1").Return(outbound.SupplySnapshot{}, errors.New("booking down"))
		_, err := uc.Join(context.Background(), inbound.JoinInput{EventID: "ev1", ClientIP: "1.2.3.4"})
		assert.ErrorIs(t, err, entity.ErrDependencyUnavailable)
	})
}

// TC-QUE-39 (P14, 4.7): the rate limiter runs before the sale check, and a rejected join still counts.
func TestJoin_SaleGate_RateLimitPrecedesAndCountsRejections(t *testing.T) {
	t.Run("limiter denies before any sale read", func(t *testing.T) {
		uc, m := newJoinRaw(t)
		m.limiter.EXPECT().Allow(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).Return(false, nil)
		_, err := uc.Join(context.Background(), inbound.JoinInput{EventID: "ev1", ClientIP: "1.2.3.4"})
		require.Error(t, err)
		assert.NotErrorIs(t, err, entity.ErrNotOnSale)
	})
	t.Run("rejected joins are counted by the limiter", func(t *testing.T) {
		uc, m := newJoinRaw(t)
		expectLimiterAndSettings(m, 3) // Allow is called once per attempt even though every attempt is rejected
		m.store.EXPECT().GetSale(gomock.Any(), "ev1").Return(outbound.SaleState{SalesStartsMs: ms(joinNow.Add(time.Hour)), SalesEndsMs: ms(joinNow.Add(2 * time.Hour))}, nil).Times(3)
		for i := 0; i < 3; i++ {
			_, err := uc.Join(context.Background(), inbound.JoinInput{EventID: "ev1", ClientIP: "1.2.3.4"})
			assert.ErrorIs(t, err, entity.ErrNotOnSale)
		}
	})
}

// errPastGate stops a join right after the sale gate: Store.Join is the first call that follows checkSale, so a
// join that reaches it has passed the gate.
var errPastGate = errors.New("past the sale gate")

// C8.6 (PF-P1-20): 50 concurrent joins on a cold cache make exactly one Booking read; every join is decided by the
// window that read produced.
func TestJoin_ColdSaleWindowOneBookingCall(t *testing.T) {
	const joins = 50
	starts, ends := joinNow.Add(-time.Hour), joinNow.Add(time.Hour)
	uc, m := newJoinRaw(t)
	m.settings.EXPECT().Get(gomock.Any(), "ev1").Return(entity.EventSettings{}, true, nil).AnyTimes()
	m.store.EXPECT().GetSale(gomock.Any(), "ev1").Return(outbound.SaleState{}, nil).AnyTimes()
	m.booking.EXPECT().Supply(gomock.Any(), "ev1").DoAndReturn(func(context.Context, string) (outbound.SupplySnapshot, error) {
		// Every concurrent join reaches the cold path while this read is still in flight.
		time.Sleep(100 * time.Millisecond)
		return outbound.SupplySnapshot{SalesStartsAt: starts, SalesEndsAt: ends}, nil
	}).Times(1)
	m.store.EXPECT().SetSaleWindow(gomock.Any(), "ev1", ms(starts), ms(ends)).Return(nil).Times(1)
	m.store.EXPECT().Join(gomock.Any(), gomock.Any()).Return(outbound.JoinResult{}, errPastGate).Times(joins)

	var wg sync.WaitGroup
	errs := make([]error, joins)
	for i := range errs {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			_, errs[i] = uc.Join(context.Background(), inbound.JoinInput{EventID: "ev1"})
		}(i)
	}
	wg.Wait()
	for i, err := range errs {
		assert.ErrorIs(t, err, errPastGate, "join %d", i)
	}
}

// C8.6 (PF-P1-20): a window Booking reports without bounds is cached with its time and stays known for 30 s, so the
// joins in that span make no Booking call.
func TestJoin_ZeroWindowIsKnownFor30s(t *testing.T) {
	tests := []struct {
		name       string
		windowAtMs int64 // when the zero window was cached (0 = never)
		wantSupply bool
	}{
		{"cached 10 s ago is known", ms(joinNow.Add(-10 * time.Second)), false},
		{"cached 29.999 s ago is known", ms(joinNow.Add(-30*time.Second + time.Millisecond)), false},
		{"cached exactly 30 s ago is unknown", ms(joinNow.Add(-30 * time.Second)), true},
		{"never cached is unknown", 0, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			uc, m := newJoinRaw(t)
			expectLimiterAndSettings(m, 1)
			m.store.EXPECT().GetSale(gomock.Any(), "ev1").Return(outbound.SaleState{WindowAtMs: tt.windowAtMs}, nil)
			if tt.wantSupply {
				m.booking.EXPECT().Supply(gomock.Any(), "ev1").Return(outbound.SupplySnapshot{}, nil).Times(1)
				m.store.EXPECT().SetSaleWindow(gomock.Any(), "ev1", int64(0), int64(0)).Return(nil).Times(1)
			} // else: Booking.Supply has no expectation, so any call fails the test
			m.store.EXPECT().Join(gomock.Any(), gomock.Any()).Return(outbound.JoinResult{}, errPastGate)
			_, err := uc.Join(context.Background(), inbound.JoinInput{EventID: "ev1", ClientIP: "1.2.3.4"})
			assert.ErrorIs(t, err, errPastGate)
		})
	}
}
