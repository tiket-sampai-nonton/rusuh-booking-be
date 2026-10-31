package usecase_test

import (
	"encoding/json"
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

func newCreate(m mocks) *usecase.CreateAttempt {
	return usecase.NewCreateAttempt(m.booking, m.attempts, m.idem, m.gateway, clk, methods, usecase.ReturnURLs{Success: "https://fe/ok", Failed: "https://fe/fail"})
}

func req(method string) dto.CreateAttemptRequest { return dto.CreateAttemptRequest{Method: method} }

func codeOf(t *testing.T, err error) apperror.Code {
	t.Helper()
	var ae *apperror.Error
	require.True(t, errors.As(err, &ae), "bukan apperror: %v", err)
	return ae.Code
}

// expectFresh sets up the calls every new (non-replayed) request makes up to and including the idempotency claim.
func expectFresh(m mocks) {
	m.booking.EXPECT().GetPayable(gomock.Any(), bookingID).Return(payable(), nil)
	m.idem.EXPECT().Begin(gomock.Any(), "create_attempt", visitorID, idemKey, gomock.Any(), gomock.Any()).Return(outbound.IdempotencyRecord{}, false, nil)
}

func TestCreateAttempt_HappyPath(t *testing.T) {
	m := newMocks(t)
	expectFresh(m)
	m.booking.EXPECT().Lock(gomock.Any(), bookingID, idemKey).Return(lockOK(), nil)
	m.attempts.EXPECT().FindOpen(gomock.Any(), bookingID).Return(entity.Attempt{}, false, nil)
	m.attempts.EXPECT().CreateNext(gomock.Any(), outbound.NewAttempt{
		BookingID: bookingID, PublicRef: "RB-7F3A9C21", Method: vo.MethodVABCA, AmountMinor: 1500000,
		ExpiresAt: now.Add(8 * time.Minute), BookingVersion: 4,
	}).Return(attempt(vo.AttemptCreated), nil)
	ins := entity.Instruction{Type: "SNAP", SnapToken: "tok-1", RedirectURL: "https://snap/r/tok-1"}
	m.gateway.EXPECT().Charge(gomock.Any(), gomock.Any()).Return(outbound.ChargeResult{ProviderTxID: "tx-1", Instruction: ins}, nil)
	stored := ins
	stored.SuccessReturnURL, stored.FailedReturnURL = "https://fe/ok", "https://fe/fail"
	m.attempts.EXPECT().Transition(gomock.Any(), outbound.TransitionSpec{
		AttemptID: attemptID, To: vo.AttemptPending, ProviderTxID: "tx-1", Instruction: &stored, LastProviderStatus: "pending",
	}).Return(true, nil)
	m.idem.EXPECT().Complete(gomock.Any(), "create_attempt", visitorID, idemKey, 201, gomock.Any()).Return(nil)

	got, err := newCreate(m).CreateAttempt(ctx(), payer, bookingID, idemKey, req("VA_BCA"))
	require.NoError(t, err)
	assert.Equal(t, "PENDING", got.Status)
	assert.Equal(t, 4, got.BookingVersion)
	assert.Equal(t, "https://snap/r/tok-1", got.RedirectURL)
	assert.Equal(t, "https://fe/ok", got.SuccessReturnURL)
	assert.Equal(t, "https://fe/fail", got.FailedReturnURL)
}

// Property 2 dan 5: tidak ada charge sebelum lock OK; nominal dan expiry berasal dari hasil lock.
func TestCreateAttempt_ChargeUsesLockedAmountAndBookingDeadline(t *testing.T) {
	m := newMocks(t)
	expectFresh(m)
	lock := lockOK()
	lock.AmountMinor = 1200000 // pesanan diubah: nominal terkunci berbeda dari payable
	lock.PaymentExpiresAt = now.Add(4*time.Minute + 20*time.Second)
	gomock.InOrder(
		m.booking.EXPECT().Lock(gomock.Any(), bookingID, idemKey).Return(lock, nil),
		m.attempts.EXPECT().FindOpen(gomock.Any(), bookingID).Return(entity.Attempt{}, false, nil),
		m.attempts.EXPECT().CreateNext(gomock.Any(), gomock.Any()).DoAndReturn(func(_ any, n outbound.NewAttempt) (entity.Attempt, error) {
			assert.EqualValues(t, 1200000, n.AmountMinor)
			a := attempt(vo.AttemptCreated)
			a.AmountMinor = n.AmountMinor
			return a, nil
		}),
		m.gateway.EXPECT().Charge(gomock.Any(), gomock.Any()).DoAndReturn(func(_ any, r outbound.ChargeRequest) (outbound.ChargeResult, error) {
			assert.EqualValues(t, 1200000, r.AmountMinor, "nominal = versi terkunci")
			assert.Equal(t, 4*time.Minute+20*time.Second, r.Expiry, "expiry gateway tidak melewati deadline booking")
			assert.Equal(t, now, r.OrderTime)
			return outbound.ChargeResult{Instruction: entity.Instruction{Type: "SNAP", RedirectURL: "https://snap/r"}}, nil
		}),
		m.attempts.EXPECT().Transition(gomock.Any(), gomock.Any()).Return(true, nil),
	)
	m.idem.EXPECT().Complete(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).Return(nil)

	_, err := newCreate(m).CreateAttempt(ctx(), payer, bookingID, idemKey, req("VA_BCA"))
	require.NoError(t, err)
}

func TestCreateAttempt_NoChargeWhenLockRefusesOrTimeIsAlmostUp(t *testing.T) {
	cases := map[string]outbound.PaymentLock{
		"booking tidak payable": {Outcome: outbound.LockBookingNotPayable},
		"payment expired":       {Outcome: outbound.LockPaymentExpired},
		"sisa waktu < 30 detik": {Outcome: outbound.LockOK, AmountMinor: 1, PaymentExpiresAt: now.Add(20 * time.Second)},
	}
	for name, lock := range cases {
		t.Run(name, func(t *testing.T) {
			m := newMocks(t)
			expectFresh(m)
			m.booking.EXPECT().Lock(gomock.Any(), bookingID, idemKey).Return(lock, nil)
			m.idem.EXPECT().Release(gomock.Any(), "create_attempt", visitorID, idemKey).Return(nil)
			// Tidak ada ekspektasi FindOpen/CreateNext/Charge: gomock gagal bila dipanggil.
			_, err := newCreate(m).CreateAttempt(ctx(), payer, bookingID, idemKey, req("VA_BCA"))
			assert.Equal(t, inbound.CodeNotPayable, codeOf(t, err))
		})
	}
}

func TestCreateAttempt_RejectsBeforeTouchingAnything(t *testing.T) {
	m := newMocks(t)
	uc := newCreate(m)
	_, err := uc.CreateAttempt(ctx(), payer, bookingID, "pendek", req("VA_BCA"))
	assert.Equal(t, apperror.CodeValidationFailed, codeOf(t, err))
	_, err = uc.CreateAttempt(ctx(), payer, bookingID, idemKey, req("CREDIT_CARD"))
	assert.Equal(t, apperror.CodeValidationFailed, codeOf(t, err))
	_, err = uc.CreateAttempt(ctx(), payer, bookingID, idemKey, req("VA_BRI")) // dikenal tetapi tidak aktif
	assert.Equal(t, inbound.CodeMethodUnavailable, codeOf(t, err))
}

func TestCreateAttempt_OwnershipAndPayableChecks(t *testing.T) {
	other := payable()
	other.VisitorID = "99999999-9999-9999-9999-999999999999"
	expired := payable()
	expired.PaymentExpiresAt = now.Add(-time.Second)
	paid := payable()
	paid.Status = "PAID"

	cases := map[string]struct {
		payer inbound.Payer
		p     outbound.Payable
		want  apperror.Code
	}{
		"booking milik visitor lain":      {payer, other, apperror.CodeNotFound},
		"token rb_pay untuk booking lain": {inbound.Payer{VisitorID: visitorID, BookingID: "44444444-4444-4444-4444-444444444444"}, payable(), apperror.CodeNotFound},
		"deadline lewat":                  {payer, expired, inbound.CodeNotPayable},
		"sudah dibayar":                   {payer, paid, inbound.CodeNotPayable},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			m := newMocks(t)
			m.booking.EXPECT().GetPayable(gomock.Any(), bookingID).Return(c.p, nil).AnyTimes()
			_, err := newCreate(m).CreateAttempt(ctx(), c.payer, bookingID, idemKey, req("VA_BCA"))
			assert.Equal(t, c.want, codeOf(t, err))
		})
	}

	m := newMocks(t)
	_, err := newCreate(m).CreateAttempt(ctx(), payer, "bukan-uuid", idemKey, req("VA_BCA"))
	assert.Equal(t, apperror.CodeNotFound, codeOf(t, err))

	m = newMocks(t)
	m.booking.EXPECT().GetPayable(gomock.Any(), bookingID).Return(outbound.Payable{}, outbound.ErrBookingUnavailable)
	_, err = newCreate(m).CreateAttempt(ctx(), payer, bookingID, idemKey, req("VA_BCA"))
	assert.Equal(t, apperror.CodeDependencyUnavailable, codeOf(t, err))
}

func TestCreateAttempt_IdempotentReplay(t *testing.T) {
	stored := dto.Attempt{AttemptNo: 1, Method: "VA_BCA", Status: "PENDING", BookingVersion: 4, RedirectURL: "https://snap/r/812"}
	body, _ := json.Marshal(stored)
	uc := func(m mocks) *usecase.CreateAttempt { return newCreate(m) }

	t.Run("sama dan selesai → respons tersimpan tanpa charge", func(t *testing.T) {
		m := newMocks(t)
		m.booking.EXPECT().GetPayable(gomock.Any(), bookingID).Return(payable(), nil)
		var hash []byte
		m.idem.EXPECT().Begin(gomock.Any(), "create_attempt", visitorID, idemKey, gomock.Any(), gomock.Any()).
			DoAndReturn(func(_ any, _, _, _ string, h []byte, _ time.Duration) (outbound.IdempotencyRecord, bool, error) {
				hash = h
				return outbound.IdempotencyRecord{State: "COMPLETED", RequestHash: h, ResponseBody: body}, true, nil
			})
		got, err := uc(m).CreateAttempt(ctx(), payer, bookingID, idemKey, req("VA_BCA"))
		require.NoError(t, err)
		assert.Equal(t, stored, got)
		assert.NotEmpty(t, hash)
	})
	t.Run("key sama payload beda → 409", func(t *testing.T) {
		m := newMocks(t)
		m.booking.EXPECT().GetPayable(gomock.Any(), bookingID).Return(payable(), nil)
		m.idem.EXPECT().Begin(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).
			Return(outbound.IdempotencyRecord{State: "COMPLETED", RequestHash: []byte("hash-lain"), ResponseBody: body}, true, nil)
		_, err := uc(m).CreateAttempt(ctx(), payer, bookingID, idemKey, req("QRIS"))
		assert.Equal(t, inbound.CodeIdemReused, codeOf(t, err))
	})
	t.Run("masih berjalan → coba lagi", func(t *testing.T) {
		m := newMocks(t)
		m.booking.EXPECT().GetPayable(gomock.Any(), bookingID).Return(payable(), nil)
		m.idem.EXPECT().Begin(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).
			DoAndReturn(func(_ any, _, _, _ string, h []byte, _ time.Duration) (outbound.IdempotencyRecord, bool, error) {
				return outbound.IdempotencyRecord{State: "IN_PROGRESS", RequestHash: h}, true, nil
			})
		_, err := uc(m).CreateAttempt(ctx(), payer, bookingID, idemKey, req("VA_BCA"))
		assert.Equal(t, apperror.CodeDependencyUnavailable, codeOf(t, err))
	})
}

// Property 6: timeout ambigu → UNKNOWN pada order yang sama; tidak ada order baru.
func TestCreateAttempt_ChargeFailuresCloseTheAttemptAccordingly(t *testing.T) {
	cases := map[string]struct {
		cause error
		to    vo.AttemptStatus
	}{
		"ambigu → UNKNOWN":           {outbound.ErrGatewayAmbiguous, vo.AttemptUnknown},
		"tidak terkirim → CANCELLED": {outbound.ErrGatewayUnavailable, vo.AttemptCancelled},
		"ditolak → FAILED":           {outbound.ErrGatewayRejected, vo.AttemptFailed},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			m := newMocks(t)
			expectFresh(m)
			m.booking.EXPECT().Lock(gomock.Any(), bookingID, idemKey).Return(lockOK(), nil)
			m.attempts.EXPECT().FindOpen(gomock.Any(), bookingID).Return(entity.Attempt{}, false, nil)
			m.attempts.EXPECT().CreateNext(gomock.Any(), gomock.Any()).Return(attempt(vo.AttemptCreated), nil).Times(1)
			m.gateway.EXPECT().Charge(gomock.Any(), gomock.Any()).Return(outbound.ChargeResult{}, c.cause).Times(1)
			m.attempts.EXPECT().Transition(gomock.Any(), gomock.Any()).DoAndReturn(func(_ any, s outbound.TransitionSpec) (bool, error) {
				assert.Equal(t, c.to, s.To)
				assert.Nil(t, s.Event)
				return true, nil
			})
			m.idem.EXPECT().Release(gomock.Any(), "create_attempt", visitorID, idemKey).Return(nil)

			_, err := newCreate(m).CreateAttempt(ctx(), payer, bookingID, idemKey, req("VA_BCA"))
			assert.Equal(t, inbound.CodeProviderUnavailable, codeOf(t, err))
		})
	}
}

func TestCreateAttempt_SameMethodReturnsExistingInstructionWithoutCharging(t *testing.T) {
	m := newMocks(t)
	expectFresh(m)
	m.booking.EXPECT().Lock(gomock.Any(), bookingID, idemKey).Return(lockOK(), nil)
	open := attempt(vo.AttemptPending)
	open.Instruction = &entity.Instruction{Type: "SNAP", RedirectURL: "https://snap/r/812"}
	m.attempts.EXPECT().FindOpen(gomock.Any(), bookingID).Return(open, true, nil)
	m.idem.EXPECT().Complete(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), 201, gomock.Any()).Return(nil)

	got, err := newCreate(m).CreateAttempt(ctx(), payer, bookingID, idemKey, req("VA_BCA"))
	require.NoError(t, err)
	assert.Equal(t, "https://snap/r/812", got.RedirectURL)
}

func TestCreateAttempt_ChangingMethodCancelsTheOldOrderFirst(t *testing.T) {
	m := newMocks(t)
	expectFresh(m)
	m.booking.EXPECT().Lock(gomock.Any(), bookingID, idemKey).Return(lockOK(), nil)
	old := attempt(vo.AttemptPending)
	m.attempts.EXPECT().FindOpen(gomock.Any(), bookingID).Return(old, true, nil)
	next := attempt(vo.AttemptCreated)
	next.AttemptNo, next.Method, next.ProviderOrderID = 2, vo.MethodQRIS, "RB-7F3A9C21-2"
	next.ID = "55555555-5555-5555-5555-555555555555"

	gomock.InOrder(
		m.gateway.EXPECT().Status(gomock.Any(), "RB-7F3A9C21-1").Return(outbound.GatewayStatus{Status: vo.AttemptPending}, nil),
		m.gateway.EXPECT().Cancel(gomock.Any(), "RB-7F3A9C21-1").Return(nil),
		m.attempts.EXPECT().Transition(gomock.Any(), gomock.Any()).DoAndReturn(func(_ any, s outbound.TransitionSpec) (bool, error) {
			assert.Equal(t, attemptID, s.AttemptID)
			assert.Equal(t, vo.AttemptCancelled, s.To)
			return true, nil
		}),
		m.attempts.EXPECT().CreateNext(gomock.Any(), gomock.Any()).Return(next, nil),
		m.gateway.EXPECT().Charge(gomock.Any(), gomock.Any()).DoAndReturn(func(_ any, r outbound.ChargeRequest) (outbound.ChargeResult, error) {
			assert.Equal(t, "RB-7F3A9C21-2", r.OrderID, "order baru, bukan order lama")
			return outbound.ChargeResult{Instruction: entity.Instruction{Type: "SNAP", RedirectURL: "https://snap/r/qr"}}, nil
		}),
		m.attempts.EXPECT().Transition(gomock.Any(), gomock.Any()).Return(true, nil),
	)
	m.idem.EXPECT().Complete(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).Return(nil)

	got, err := newCreate(m).CreateAttempt(ctx(), payer, bookingID, idemKey, req("QRIS"))
	require.NoError(t, err)
	assert.Equal(t, 2, got.AttemptNo)
	assert.Equal(t, "https://snap/r/qr", got.RedirectURL)
}

func TestCreateAttempt_OldOrderAlreadyPaidBlocksNewAttempt(t *testing.T) {
	m := newMocks(t)
	expectFresh(m)
	m.booking.EXPECT().Lock(gomock.Any(), bookingID, idemKey).Return(lockOK(), nil)
	m.attempts.EXPECT().FindOpen(gomock.Any(), bookingID).Return(attempt(vo.AttemptUnknown), true, nil)
	settled := now.Add(-time.Minute)
	m.gateway.EXPECT().Status(gomock.Any(), "RB-7F3A9C21-1").Return(outbound.GatewayStatus{Status: vo.AttemptSucceeded, ProviderTxID: "tx", SettlementTime: &settled, RawStatus: "settlement"}, nil)
	m.attempts.EXPECT().Transition(gomock.Any(), gomock.Any()).DoAndReturn(func(_ any, s outbound.TransitionSpec) (bool, error) {
		assert.Equal(t, vo.AttemptSucceeded, s.To)
		require.NotNil(t, s.Event)
		assert.Equal(t, "payment.succeeded", s.Event.RoutingKey, "uang yang sudah masuk dicatat, bukan dibuang")
		return true, nil
	})
	m.idem.EXPECT().Release(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).Return(nil)
	// Tidak ada CreateNext/Charge.
	_, err := newCreate(m).CreateAttempt(ctx(), payer, bookingID, idemKey, req("QRIS"))
	assert.Equal(t, inbound.CodeNotPayable, codeOf(t, err))
}

func TestCreateAttempt_CannotCloseOldOrderMeansNoNewOrder(t *testing.T) {
	m := newMocks(t)
	expectFresh(m)
	m.booking.EXPECT().Lock(gomock.Any(), bookingID, idemKey).Return(lockOK(), nil)
	m.attempts.EXPECT().FindOpen(gomock.Any(), bookingID).Return(attempt(vo.AttemptPending), true, nil)
	m.gateway.EXPECT().Status(gomock.Any(), gomock.Any()).Return(outbound.GatewayStatus{Status: vo.AttemptPending}, nil)
	m.gateway.EXPECT().Cancel(gomock.Any(), gomock.Any()).Return(outbound.ErrGatewayRejected)
	m.idem.EXPECT().Release(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).Return(nil)
	_, err := newCreate(m).CreateAttempt(ctx(), payer, bookingID, idemKey, req("QRIS"))
	assert.Equal(t, inbound.CodeProviderUnavailable, codeOf(t, err), "order lama masih terbuka: jangan buat order kedua (bisa bayar dua kali)")
}

func TestCreateAttempt_ParallelRequestLosingTheRaceIsRetryable(t *testing.T) {
	m := newMocks(t)
	expectFresh(m)
	m.booking.EXPECT().Lock(gomock.Any(), bookingID, idemKey).Return(lockOK(), nil)
	m.attempts.EXPECT().FindOpen(gomock.Any(), bookingID).Return(entity.Attempt{}, false, nil)
	m.attempts.EXPECT().CreateNext(gomock.Any(), gomock.Any()).Return(entity.Attempt{}, outbound.ErrOpenAttemptExists)
	m.idem.EXPECT().Release(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).Return(nil)
	_, err := newCreate(m).CreateAttempt(ctx(), payer, bookingID, idemKey, req("VA_BCA"))
	assert.Equal(t, apperror.CodeDependencyUnavailable, codeOf(t, err))
}

func TestCreateAttempt_LockErrors(t *testing.T) {
	for name, c := range map[string]struct {
		err  error
		want apperror.Code
	}{
		"booking hilang":      {outbound.ErrBookingNotFound, apperror.CodeNotFound},
		"booking tidak sehat": {outbound.ErrBookingUnavailable, apperror.CodeDependencyUnavailable},
	} {
		t.Run(name, func(t *testing.T) {
			m := newMocks(t)
			expectFresh(m)
			m.booking.EXPECT().Lock(gomock.Any(), bookingID, idemKey).Return(outbound.PaymentLock{}, c.err)
			m.idem.EXPECT().Release(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).Return(nil)
			_, err := newCreate(m).CreateAttempt(ctx(), payer, bookingID, idemKey, req("VA_BCA"))
			assert.Equal(t, c.want, codeOf(t, err))
		})
	}
}
