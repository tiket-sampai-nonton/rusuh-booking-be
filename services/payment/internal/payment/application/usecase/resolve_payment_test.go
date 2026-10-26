package usecase_test

import (
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	"github.com/tiket-sampai-nonton/rusuh-booking-be/pkg/apperror"

	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/payment/internal/payment/application/usecase"
	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/payment/internal/payment/domain/dto"
	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/payment/internal/payment/domain/entity"
	vo "github.com/tiket-sampai-nonton/rusuh-booking-be/services/payment/internal/payment/domain/valueobject"
	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/payment/internal/payment/port/outbound"
)

func newResolve(m mocks) *usecase.ResolvePayment {
	return usecase.NewResolvePayment(m.attempts, m.gateway, clk)
}

func findLatest(m mocks, a entity.Attempt) {
	m.attempts.EXPECT().FindLatest(gomock.Any(), bookingID).Return(a, true, nil)
}

func TestResolve_NoAttemptAndFinalStatesNeverCallTheGateway(t *testing.T) {
	m := newMocks(t)
	m.attempts.EXPECT().FindLatest(gomock.Any(), bookingID).Return(entity.Attempt{}, false, nil)
	got, err := newResolve(m).ResolvePayment(ctx(), bookingID, dto.ResolveCheck)
	require.NoError(t, err)
	assert.Equal(t, dto.OutcomeNoAttempt, got.Outcome)

	m = newMocks(t)
	paid := attempt(vo.AttemptSucceeded)
	at := now.Add(-time.Minute)
	paid.SettlementTime = &at
	findLatest(m, paid)
	got, err = newResolve(m).ResolvePayment(ctx(), bookingID, dto.ResolveCheckAndCancel)
	require.NoError(t, err)
	assert.Equal(t, dto.OutcomeSucceeded, got.Outcome)
	assert.Equal(t, attemptID, got.AttemptID)
	require.NotNil(t, got.SettlementTime)
	assert.True(t, got.SettlementTime.Equal(at))

	m = newMocks(t)
	findLatest(m, attempt(vo.AttemptFailed))
	got, err = newResolve(m).ResolvePayment(ctx(), bookingID, dto.ResolveCheck)
	require.NoError(t, err)
	assert.Equal(t, dto.OutcomeNotPaid, got.Outcome)
}

// Property 8: resolve tidak pernah memblokir; gateway sakit → UNKNOWN tanpa error.
func TestResolve_GatewayDownReturnsUnknownWithoutError(t *testing.T) {
	for _, gerr := range []error{outbound.ErrGatewayUnavailable, outbound.ErrGatewayAmbiguous} {
		m := newMocks(t)
		findLatest(m, attempt(vo.AttemptPending))
		m.gateway.EXPECT().Status(gomock.Any(), "RB-7F3A9C21-1").Return(outbound.GatewayStatus{}, gerr)
		got, err := newResolve(m).ResolvePayment(ctx(), bookingID, dto.ResolveCheckAndCancel)
		require.NoError(t, err)
		assert.Equal(t, dto.OutcomeUnknown, got.Outcome)
	}
}

func TestResolve_SettlementFoundIsStoredOnceAndReported(t *testing.T) {
	m := newMocks(t)
	findLatest(m, attempt(vo.AttemptUnknown))
	settled := time.Date(2026, 10, 26, 9, 55, 0, 0, time.UTC)
	m.gateway.EXPECT().Status(gomock.Any(), gomock.Any()).Return(outbound.GatewayStatus{Status: vo.AttemptSucceeded, ProviderTxID: "tx", SettlementTime: &settled, RawStatus: "settlement"}, nil)
	m.attempts.EXPECT().Transition(gomock.Any(), gomock.Any()).DoAndReturn(func(_ any, s outbound.TransitionSpec) (bool, error) {
		assert.Equal(t, vo.AttemptSucceeded, s.To)
		require.NotNil(t, s.Event)
		assert.Equal(t, "payment.succeeded", s.Event.RoutingKey, "Booking menerima event yang sama seperti dari webhook")
		return true, nil
	})
	stored := attempt(vo.AttemptSucceeded)
	stored.SettlementTime = &settled
	findLatest(m, stored)

	got, err := newResolve(m).ResolvePayment(ctx(), bookingID, dto.ResolveCheck)
	require.NoError(t, err)
	assert.Equal(t, dto.OutcomeSucceeded, got.Outcome)
	require.NotNil(t, got.SettlementTime)
	assert.True(t, got.SettlementTime.Equal(settled))
}

func TestResolve_CheckNeverCancels(t *testing.T) {
	m := newMocks(t)
	findLatest(m, attempt(vo.AttemptPending))
	m.gateway.EXPECT().Status(gomock.Any(), gomock.Any()).Return(outbound.GatewayStatus{Status: vo.AttemptPending}, nil)
	got, err := newResolve(m).ResolvePayment(ctx(), bookingID, dto.ResolveCheck) // tanpa Cancel
	require.NoError(t, err)
	assert.Equal(t, dto.OutcomeNotPaid, got.Outcome)
}

func TestResolve_CheckAndCancel(t *testing.T) {
	t.Run("dibatalkan dan terkonfirmasi → NOT_PAID", func(t *testing.T) {
		m := newMocks(t)
		findLatest(m, attempt(vo.AttemptPending))
		gomock.InOrder(
			m.gateway.EXPECT().Status(gomock.Any(), gomock.Any()).Return(outbound.GatewayStatus{Status: vo.AttemptPending}, nil),
			m.gateway.EXPECT().Cancel(gomock.Any(), "RB-7F3A9C21-1").Return(nil),
			m.gateway.EXPECT().Status(gomock.Any(), gomock.Any()).Return(outbound.GatewayStatus{Status: vo.AttemptCancelled, RawStatus: "cancel"}, nil),
			m.attempts.EXPECT().Transition(gomock.Any(), gomock.Any()).DoAndReturn(func(_ any, s outbound.TransitionSpec) (bool, error) {
				assert.Equal(t, vo.AttemptCancelled, s.To)
				return true, nil
			}),
		)
		got, err := newResolve(m).ResolvePayment(ctx(), bookingID, dto.ResolveCheckAndCancel)
		require.NoError(t, err)
		assert.Equal(t, dto.OutcomeNotPaid, got.Outcome)
	})
	t.Run("dibayar di sela pembatalan → SUCCEEDED", func(t *testing.T) {
		m := newMocks(t)
		findLatest(m, attempt(vo.AttemptPending))
		settled := now.Add(-time.Second)
		gomock.InOrder(
			m.gateway.EXPECT().Status(gomock.Any(), gomock.Any()).Return(outbound.GatewayStatus{Status: vo.AttemptPending}, nil),
			m.gateway.EXPECT().Cancel(gomock.Any(), gomock.Any()).Return(outbound.ErrGatewayRejected), // 412: sudah settle
			m.gateway.EXPECT().Status(gomock.Any(), gomock.Any()).Return(outbound.GatewayStatus{Status: vo.AttemptSucceeded, SettlementTime: &settled}, nil),
			m.attempts.EXPECT().Transition(gomock.Any(), gomock.Any()).Return(true, nil),
		)
		stored := attempt(vo.AttemptSucceeded)
		stored.SettlementTime = &settled
		findLatest(m, stored)
		got, err := newResolve(m).ResolvePayment(ctx(), bookingID, dto.ResolveCheckAndCancel)
		require.NoError(t, err)
		assert.Equal(t, dto.OutcomeSucceeded, got.Outcome)
	})
	t.Run("cancel ditolak dan order masih terbuka → UNKNOWN (stok tidak boleh dilepas)", func(t *testing.T) {
		m := newMocks(t)
		findLatest(m, attempt(vo.AttemptPending))
		m.gateway.EXPECT().Status(gomock.Any(), gomock.Any()).Return(outbound.GatewayStatus{Status: vo.AttemptPending}, nil).Times(2)
		m.gateway.EXPECT().Cancel(gomock.Any(), gomock.Any()).Return(outbound.ErrGatewayRejected)
		got, err := newResolve(m).ResolvePayment(ctx(), bookingID, dto.ResolveCheckAndCancel)
		require.NoError(t, err)
		assert.Equal(t, dto.OutcomeUnknown, got.Outcome)
	})
	t.Run("cancel tidak terjawab → UNKNOWN", func(t *testing.T) {
		m := newMocks(t)
		findLatest(m, attempt(vo.AttemptPending))
		m.gateway.EXPECT().Status(gomock.Any(), gomock.Any()).Return(outbound.GatewayStatus{Status: vo.AttemptPending}, nil)
		m.gateway.EXPECT().Cancel(gomock.Any(), gomock.Any()).Return(outbound.ErrGatewayAmbiguous)
		got, err := newResolve(m).ResolvePayment(ctx(), bookingID, dto.ResolveCheckAndCancel)
		require.NoError(t, err)
		assert.Equal(t, dto.OutcomeUnknown, got.Outcome)
	})
}

func TestResolve_OrderUnknownToMidtransMeansNothingWasPaid(t *testing.T) {
	m := newMocks(t)
	findLatest(m, attempt(vo.AttemptCreated))
	m.gateway.EXPECT().Status(gomock.Any(), gomock.Any()).Return(outbound.GatewayStatus{}, outbound.ErrOrderNotFound)
	m.attempts.EXPECT().Transition(gomock.Any(), gomock.Any()).DoAndReturn(func(_ any, s outbound.TransitionSpec) (bool, error) {
		assert.Equal(t, vo.AttemptCancelled, s.To)
		return true, nil
	})
	got, err := newResolve(m).ResolvePayment(ctx(), bookingID, dto.ResolveCheckAndCancel)
	require.NoError(t, err)
	assert.Equal(t, dto.OutcomeNotPaid, got.Outcome)
}

func TestResolve_ValidatesInputAndLookupErrors(t *testing.T) {
	m := newMocks(t)
	_, err := newResolve(m).ResolvePayment(ctx(), "bukan-uuid", dto.ResolveCheck)
	assert.Equal(t, apperror.CodeValidationFailed, codeOf(t, err))
	_, err = newResolve(m).ResolvePayment(ctx(), bookingID, "CANCEL")
	assert.Equal(t, apperror.CodeValidationFailed, codeOf(t, err))

	m.attempts.EXPECT().FindLatest(gomock.Any(), bookingID).Return(entity.Attempt{}, false, errors.New("db down"))
	_, err = newResolve(m).ResolvePayment(ctx(), bookingID, dto.ResolveCheck)
	assert.Equal(t, apperror.CodeDependencyUnavailable, codeOf(t, err))
}
