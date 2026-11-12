package usecase_test

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/booking/internal/booking/application/usecase"
	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/booking/internal/booking/port/outbound"
)

func TestPaymentLock_OK(t *testing.T) {
	uow := &fakeUoW{
		lockResult: outbound.PaymentLockResult{
			Outcome: "OK", Version: 2, AmountMinor: 1000,
			PaymentExpiresAt: time.Now().Add(time.Hour),
		},
	}
	uc := usecase.NewPaymentLock(uow, fixedClock{t: time.Now()})
	resp, err := uc.Lock(context.Background(), "bk-1", "attempt-key-abc123")
	require.NoError(t, err)
	assert.Equal(t, "OK", resp.Outcome)
	assert.Equal(t, 1, uow.lockCalls)
}

func TestPaymentLock_NotPayable(t *testing.T) {
	uow := &fakeUoW{lockResult: outbound.PaymentLockResult{Outcome: "BOOKING_NOT_PAYABLE"}}
	uc := usecase.NewPaymentLock(uow, fixedClock{t: time.Now()})
	resp, err := uc.Lock(context.Background(), "bk-1", "attempt-key-abc123")
	require.NoError(t, err)
	assert.Equal(t, "BOOKING_NOT_PAYABLE", resp.Outcome)
}

func TestPaymentLock_Expired(t *testing.T) {
	uow := &fakeUoW{lockResult: outbound.PaymentLockResult{Outcome: "PAYMENT_EXPIRED"}}
	uc := usecase.NewPaymentLock(uow, fixedClock{t: time.Now()})
	resp, err := uc.Lock(context.Background(), "bk-1", "attempt-key-abc123")
	require.NoError(t, err)
	assert.Equal(t, "PAYMENT_EXPIRED", resp.Outcome)
}

// lockSpyUoW records the spec that PaymentLock hands to the unit of work.
type lockSpyUoW struct {
	*fakeUoW
	spec outbound.PaymentLockSpec
}

func (f *lockSpyUoW) PaymentLock(ctx context.Context, spec outbound.PaymentLockSpec) (outbound.PaymentLockResult, error) {
	f.spec = spec
	return f.fakeUoW.PaymentLock(ctx, spec)
}

func TestPaymentLock_PassesClock(t *testing.T) {
	at := time.Date(2026, 11, 13, 9, 0, 0, 0, time.UTC)
	uow := &lockSpyUoW{fakeUoW: &fakeUoW{lockResult: outbound.PaymentLockResult{Outcome: "OK"}}}
	uc := usecase.NewPaymentLock(uow, fixedClock{t: at})
	_, err := uc.Lock(context.Background(), "bk-1", "attempt-key-abc123")
	require.NoError(t, err)
	assert.Equal(t, at, uow.spec.Now)
}

func TestPaymentLock_EmptyAttemptKey(t *testing.T) {
	uc := usecase.NewPaymentLock(&fakeUoW{}, fixedClock{t: time.Now()})
	_, err := uc.Lock(context.Background(), "bk-1", "")
	assert.Error(t, err)
}

func TestPaymentLock_ShortAttemptKey(t *testing.T) {
	uc := usecase.NewPaymentLock(&fakeUoW{}, fixedClock{t: time.Now()})
	_, err := uc.Lock(context.Background(), "bk-1", "short")
	assert.Error(t, err)
}
