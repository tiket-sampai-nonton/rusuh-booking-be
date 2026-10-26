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
	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/payment/internal/payment/port/inbound"
	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/payment/internal/payment/port/outbound"
)

func newWebhook(m mocks) *usecase.HandleWebhook {
	return usecase.NewHandleWebhook(m.verifier, m.attempts, m.cbs, clk)
}

func notification(status string) dto.MidtransNotification {
	return dto.MidtransNotification{
		OrderID: "RB-7F3A9C21-1", TransactionStatus: status, StatusCode: "200", GrossAmount: "1500000.00",
		SignatureKey: "sig", TransactionID: "tx-1", SettlementTime: "2026-10-26 17:30:05", FraudStatus: "accept",
	}
}

func validSignature(m mocks) {
	m.verifier.EXPECT().Verify("RB-7F3A9C21-1", "200", gomock.Any(), "sig").Return(true).AnyTimes()
}

func expectCallback(t *testing.T, m mocks, result string) {
	t.Helper()
	m.cbs.EXPECT().Record(gomock.Any(), gomock.Any()).DoAndReturn(func(_ any, cb outbound.Callback) (bool, error) {
		assert.Equal(t, "MIDTRANS", cb.Provider)
		assert.Equal(t, result, cb.Result)
		assert.NotEmpty(t, cb.Fingerprint)
		assert.NotEmpty(t, cb.PayloadDigest)
		return true, nil
	})
}

// Property 3: signature is the only proof.
func TestWebhook_InvalidSignatureIsRejectedAndChangesNothing(t *testing.T) {
	m := newMocks(t)
	m.verifier.EXPECT().Verify(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).Return(false)
	m.cbs.EXPECT().Record(gomock.Any(), gomock.Any()).DoAndReturn(func(_ any, cb outbound.Callback) (bool, error) {
		assert.False(t, cb.SignatureValid)
		assert.Equal(t, outbound.CallbackRejected, cb.Result)
		return true, nil
	})
	// Tidak ada FindByOrderID/Transition.
	err := newWebhook(m).HandleWebhook(ctx(), notification("settlement"), []byte(`{}`))
	assert.Equal(t, inbound.CodeSignatureInvalid, codeOf(t, err))
}

func TestWebhook_RejectedCallbackStillAnswers401WhenStoringFails(t *testing.T) {
	m := newMocks(t)
	m.verifier.EXPECT().Verify(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).Return(false)
	m.cbs.EXPECT().Record(gomock.Any(), gomock.Any()).Return(false, errors.New("db down"))
	err := newWebhook(m).HandleWebhook(ctx(), notification("settlement"), []byte(`{}`))
	assert.Equal(t, inbound.CodeSignatureInvalid, codeOf(t, err))
}

func TestWebhook_SettlementMovesToSucceededAndWritesEvent(t *testing.T) {
	m := newMocks(t)
	validSignature(m)
	m.attempts.EXPECT().FindByOrderID(gomock.Any(), "RB-7F3A9C21-1").Return(attempt(vo.AttemptPending), true, nil)
	m.attempts.EXPECT().Transition(gomock.Any(), gomock.Any()).DoAndReturn(func(_ any, s outbound.TransitionSpec) (bool, error) {
		assert.Equal(t, attemptID, s.AttemptID)
		assert.Equal(t, vo.AttemptSucceeded, s.To)
		assert.Equal(t, "tx-1", s.ProviderTxID)
		require.NotNil(t, s.SettlementTime)
		assert.Equal(t, time.Date(2026, 10, 26, 10, 30, 5, 0, time.UTC), *s.SettlementTime, "settlement_time WIB → UTC")
		require.NotNil(t, s.Event)
		assert.Equal(t, "payment.succeeded", s.Event.RoutingKey)
		assert.Equal(t, dto.PaymentSucceeded{
			BookingID: bookingID, AttemptID: attemptID, SettlementTime: *s.SettlementTime, AmountMinor: 1500000, Method: "VA_BCA",
		}, s.Event.Payload)
		return true, nil
	})
	expectCallback(t, m, outbound.CallbackProcessed)

	require.NoError(t, newWebhook(m).HandleWebhook(ctx(), notification("settlement"), []byte(`{"order_id":"x"}`)))
}

// Property 4: duplikat dan out-of-order aman.
func TestWebhook_DuplicateOrOutOfOrderChangesNothingButIsAcknowledged(t *testing.T) {
	m := newMocks(t)
	validSignature(m)
	m.attempts.EXPECT().FindByOrderID(gomock.Any(), gomock.Any()).Return(attempt(vo.AttemptSucceeded), true, nil)
	m.attempts.EXPECT().Transition(gomock.Any(), gomock.Any()).Return(false, nil) // sudah SUCCEEDED
	expectCallback(t, m, outbound.CallbackDuplicate)
	require.NoError(t, newWebhook(m).HandleWebhook(ctx(), notification("pending"), []byte(`{}`)), "200 agar Midtrans berhenti mengulang")
}

func TestWebhook_FailureAndExpiryAndCancel(t *testing.T) {
	cases := map[string]struct {
		status string
		to     vo.AttemptStatus
		event  string
	}{
		"deny":   {"deny", vo.AttemptFailed, "payment.failed"},
		"cancel": {"cancel", vo.AttemptCancelled, ""},
		"expire": {"expire", vo.AttemptExpired, ""},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			m := newMocks(t)
			validSignature(m)
			m.attempts.EXPECT().FindByOrderID(gomock.Any(), gomock.Any()).Return(attempt(vo.AttemptPending), true, nil)
			m.attempts.EXPECT().Transition(gomock.Any(), gomock.Any()).DoAndReturn(func(_ any, s outbound.TransitionSpec) (bool, error) {
				assert.Equal(t, c.to, s.To)
				if c.event == "" {
					assert.Nil(t, s.Event)
				} else {
					require.NotNil(t, s.Event)
					assert.Equal(t, c.event, s.Event.RoutingKey)
					assert.Equal(t, dto.PaymentFailed{BookingID: bookingID, AttemptID: attemptID, Reason: "DENY"}, s.Event.Payload)
				}
				return true, nil
			})
			expectCallback(t, m, outbound.CallbackProcessed)
			require.NoError(t, newWebhook(m).HandleWebhook(ctx(), notification(c.status), []byte(`{}`)))
		})
	}
}

func TestWebhook_AmountMismatchNeverChangesState(t *testing.T) {
	for _, gross := range []string{"1499999.00", "1500000.50", "abc", "-1"} {
		t.Run(gross, func(t *testing.T) {
			m := newMocks(t)
			validSignature(m)
			n := notification("settlement")
			n.GrossAmount = gross
			m.verifier.EXPECT().Verify(gomock.Any(), gomock.Any(), gross, gomock.Any()).Return(true).AnyTimes()
			m.attempts.EXPECT().FindByOrderID(gomock.Any(), gomock.Any()).Return(attempt(vo.AttemptPending), true, nil)
			expectCallback(t, m, outbound.CallbackRejected) // tidak ada Transition
			require.NoError(t, newWebhook(m).HandleWebhook(ctx(), n, []byte(`{}`)))
		})
	}
}

func TestWebhook_GrossAmountWithoutDecimalsIsAccepted(t *testing.T) {
	m := newMocks(t)
	n := notification("settlement")
	n.GrossAmount = "1500000"
	m.verifier.EXPECT().Verify(gomock.Any(), gomock.Any(), "1500000", gomock.Any()).Return(true)
	m.attempts.EXPECT().FindByOrderID(gomock.Any(), gomock.Any()).Return(attempt(vo.AttemptPending), true, nil)
	m.attempts.EXPECT().Transition(gomock.Any(), gomock.Any()).Return(true, nil)
	expectCallback(t, m, outbound.CallbackProcessed)
	require.NoError(t, newWebhook(m).HandleWebhook(ctx(), n, []byte(`{}`)))
}

func TestWebhook_UnknownOrderAndNoProgressStatusesAreAcknowledged(t *testing.T) {
	t.Run("order tidak dikenal", func(t *testing.T) {
		m := newMocks(t)
		validSignature(m)
		m.attempts.EXPECT().FindByOrderID(gomock.Any(), gomock.Any()).Return(entity.Attempt{}, false, nil)
		expectCallback(t, m, outbound.CallbackError)
		require.NoError(t, newWebhook(m).HandleWebhook(ctx(), notification("settlement"), []byte(`{}`)))
	})
	t.Run("refund tidak mengubah state", func(t *testing.T) {
		m := newMocks(t)
		validSignature(m)
		m.attempts.EXPECT().FindByOrderID(gomock.Any(), gomock.Any()).Return(attempt(vo.AttemptSucceeded), true, nil)
		expectCallback(t, m, outbound.CallbackProcessed)
		require.NoError(t, newWebhook(m).HandleWebhook(ctx(), notification("refund"), []byte(`{}`)))
	})
	t.Run("pending sebelum respons charge tersimpan", func(t *testing.T) {
		m := newMocks(t)
		validSignature(m)
		m.attempts.EXPECT().FindByOrderID(gomock.Any(), gomock.Any()).Return(attempt(vo.AttemptCreated), true, nil)
		expectCallback(t, m, outbound.CallbackProcessed) // instruksi ditulis oleh alur charge, bukan webhook
		require.NoError(t, newWebhook(m).HandleWebhook(ctx(), notification("pending"), []byte(`{}`)))
	})
}

func TestWebhook_SettlementWithoutParsableTimeFallsBackToNow(t *testing.T) {
	m := newMocks(t)
	validSignature(m)
	n := notification("settlement")
	n.SettlementTime = ""
	m.attempts.EXPECT().FindByOrderID(gomock.Any(), gomock.Any()).Return(attempt(vo.AttemptPending), true, nil)
	m.attempts.EXPECT().Transition(gomock.Any(), gomock.Any()).DoAndReturn(func(_ any, s outbound.TransitionSpec) (bool, error) {
		require.NotNil(t, s.SettlementTime)
		assert.Equal(t, now, *s.SettlementTime)
		return true, nil
	})
	expectCallback(t, m, outbound.CallbackProcessed)
	require.NoError(t, newWebhook(m).HandleWebhook(ctx(), n, []byte(`{}`)))
}

func TestWebhook_InfrastructureFailuresAskMidtransToRetry(t *testing.T) {
	t.Run("lookup gagal", func(t *testing.T) {
		m := newMocks(t)
		validSignature(m)
		m.attempts.EXPECT().FindByOrderID(gomock.Any(), gomock.Any()).Return(entity.Attempt{}, false, errors.New("db down"))
		err := newWebhook(m).HandleWebhook(ctx(), notification("settlement"), []byte(`{}`))
		assert.Equal(t, apperror.CodeDependencyUnavailable, codeOf(t, err))
	})
	t.Run("transisi gagal", func(t *testing.T) {
		m := newMocks(t)
		validSignature(m)
		m.attempts.EXPECT().FindByOrderID(gomock.Any(), gomock.Any()).Return(attempt(vo.AttemptPending), true, nil)
		m.attempts.EXPECT().Transition(gomock.Any(), gomock.Any()).Return(false, errors.New("db down"))
		err := newWebhook(m).HandleWebhook(ctx(), notification("settlement"), []byte(`{}`))
		assert.Equal(t, apperror.CodeDependencyUnavailable, codeOf(t, err))
	})
	t.Run("callback gagal disimpan", func(t *testing.T) {
		m := newMocks(t)
		validSignature(m)
		m.attempts.EXPECT().FindByOrderID(gomock.Any(), gomock.Any()).Return(attempt(vo.AttemptPending), true, nil)
		m.attempts.EXPECT().Transition(gomock.Any(), gomock.Any()).Return(true, nil)
		m.cbs.EXPECT().Record(gomock.Any(), gomock.Any()).Return(false, errors.New("db down"))
		err := newWebhook(m).HandleWebhook(ctx(), notification("settlement"), []byte(`{}`))
		assert.Equal(t, apperror.CodeDependencyUnavailable, codeOf(t, err), "diproses ulang aman karena transisi idempoten")
	})
}

func TestWebhook_MissingFieldsAreValidationErrors(t *testing.T) {
	m := newMocks(t)
	for _, n := range []dto.MidtransNotification{{}, {OrderID: "x"}, {OrderID: "x", TransactionStatus: "settlement", StatusCode: "200"}} {
		err := newWebhook(m).HandleWebhook(ctx(), n, []byte(`{}`))
		assert.Equal(t, apperror.CodeValidationFailed, codeOf(t, err))
	}
}

// A forged callback must not occupy the audit slot of the genuine one with the same fields.
func TestWebhook_ForgedAndGenuineCallbacksHaveDifferentFingerprints(t *testing.T) {
	var forged, genuine []byte

	m := newMocks(t)
	m.verifier.EXPECT().Verify(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).Return(false)
	m.cbs.EXPECT().Record(gomock.Any(), gomock.Any()).DoAndReturn(func(_ any, cb outbound.Callback) (bool, error) { forged = cb.Fingerprint; return true, nil })
	_ = newWebhook(m).HandleWebhook(ctx(), notification("settlement"), []byte(`{}`))

	m = newMocks(t)
	validSignature(m)
	m.attempts.EXPECT().FindByOrderID(gomock.Any(), gomock.Any()).Return(attempt(vo.AttemptPending), true, nil)
	m.attempts.EXPECT().Transition(gomock.Any(), gomock.Any()).Return(true, nil)
	m.cbs.EXPECT().Record(gomock.Any(), gomock.Any()).DoAndReturn(func(_ any, cb outbound.Callback) (bool, error) { genuine = cb.Fingerprint; return true, nil })
	require.NoError(t, newWebhook(m).HandleWebhook(ctx(), notification("settlement"), []byte(`{}`)))

	require.NotEmpty(t, forged)
	require.NotEmpty(t, genuine)
	assert.NotEqual(t, forged, genuine)
}
