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
	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/payment/internal/payment/domain/entity"
	vo "github.com/tiket-sampai-nonton/rusuh-booking-be/services/payment/internal/payment/domain/valueobject"
	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/payment/internal/payment/port/inbound"
	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/payment/internal/payment/port/outbound"
)

func TestGetPage(t *testing.T) {
	m := newMocks(t)
	m.booking.EXPECT().GetPayable(gomock.Any(), bookingID).Return(payable(), nil)
	open := attempt(vo.AttemptPending)
	open.Instruction = &entity.Instruction{Type: "VA", VANumber: "812"}
	m.attempts.EXPECT().FindOpen(gomock.Any(), bookingID).Return(open, true, nil)

	got, err := usecase.NewGetPaymentPage(m.booking, m.attempts, methods, clk).GetPage(ctx(), payer, bookingID)
	require.NoError(t, err)
	assert.Equal(t, methods, got.Methods)
	assert.EqualValues(t, 1500000, got.AmountMinor)
	assert.Equal(t, now.Add(8*time.Minute), got.PaymentExpiresAt)
	assert.Equal(t, now, got.ServerTime)
	require.NotNil(t, got.CurrentAttempt)
	assert.Equal(t, "812", got.CurrentAttempt.Instruction.VANumber)

	got.Methods[0] = "DIUBAH"
	assert.Equal(t, "VA_BCA", methods[0], "daftar metode dikloning")
}

func TestGetPage_NullAttemptAndRefusals(t *testing.T) {
	m := newMocks(t)
	m.booking.EXPECT().GetPayable(gomock.Any(), bookingID).Return(payable(), nil)
	m.attempts.EXPECT().FindOpen(gomock.Any(), bookingID).Return(entity.Attempt{}, false, nil)
	got, err := usecase.NewGetPaymentPage(m.booking, m.attempts, methods, clk).GetPage(ctx(), payer, bookingID)
	require.NoError(t, err)
	assert.Nil(t, got.CurrentAttempt)

	paid := payable()
	paid.Status = "PAID"
	m = newMocks(t)
	m.booking.EXPECT().GetPayable(gomock.Any(), bookingID).Return(paid, nil)
	_, err = usecase.NewGetPaymentPage(m.booking, m.attempts, methods, clk).GetPage(ctx(), payer, bookingID)
	assert.Equal(t, inbound.CodeNotPayable, codeOf(t, err))

	other := payable()
	other.VisitorID = "99999999-9999-9999-9999-999999999999"
	m = newMocks(t)
	m.booking.EXPECT().GetPayable(gomock.Any(), bookingID).Return(other, nil)
	_, err = usecase.NewGetPaymentPage(m.booking, m.attempts, methods, clk).GetPage(ctx(), payer, bookingID)
	assert.Equal(t, apperror.CodeNotFound, codeOf(t, err), "tidak membocorkan keberadaan booking milik orang lain")

	m = newMocks(t)
	m.booking.EXPECT().GetPayable(gomock.Any(), bookingID).Return(outbound.Payable{}, outbound.ErrBookingNotFound)
	_, err = usecase.NewGetPaymentPage(m.booking, m.attempts, methods, clk).GetPage(ctx(), payer, bookingID)
	assert.Equal(t, apperror.CodeNotFound, codeOf(t, err))
}

func TestGetStatus_IsReadOnlyAndWorksAfterPaymentWindow(t *testing.T) {
	expired := payable()
	expired.Status, expired.PaymentExpiresAt = "EXPIRED", now.Add(-time.Hour)
	m := newMocks(t)
	m.booking.EXPECT().GetPayable(gomock.Any(), bookingID).Return(expired, nil)
	m.attempts.EXPECT().FindLatest(gomock.Any(), bookingID).Return(attempt(vo.AttemptExpired), true, nil)

	got, err := usecase.NewGetPaymentStatus(m.booking, m.attempts, clk).GetStatus(ctx(), payer, bookingID)
	require.NoError(t, err)
	assert.Equal(t, "EXPIRED", got.BookingStatus)
	require.NotNil(t, got.Attempt)
	assert.Equal(t, "EXPIRED", got.Attempt.Status)

	m = newMocks(t)
	m.booking.EXPECT().GetPayable(gomock.Any(), bookingID).Return(payable(), nil)
	m.attempts.EXPECT().FindLatest(gomock.Any(), bookingID).Return(entity.Attempt{}, false, errors.New("db down"))
	_, err = usecase.NewGetPaymentStatus(m.booking, m.attempts, clk).GetStatus(ctx(), payer, bookingID)
	assert.Equal(t, apperror.CodeDependencyUnavailable, codeOf(t, err))
}

func TestCreatePageToken_StoresOnlyTheHash(t *testing.T) {
	m := newMocks(t)
	m.booking.EXPECT().GetPayable(gomock.Any(), bookingID).Return(payable(), nil)
	var stored []byte
	m.tokens.EXPECT().Create(gomock.Any(), gomock.Any(), bookingID, visitorID, now.Add(60*time.Second)).
		DoAndReturn(func(_ any, h []byte, _, _ string, _ time.Time) error { stored = h; return nil })

	got, err := usecase.NewCreatePageToken(m.booking, m.tokens, clk, 60*time.Second).CreatePageToken(ctx(), payer, bookingID)
	require.NoError(t, err)
	assert.GreaterOrEqual(t, len(got.PageToken), 40)
	assert.Equal(t, "/pay/"+bookingID+"?ppt="+got.PageToken+"&embedded=1", got.PageURL)
	assert.Equal(t, now.Add(60*time.Second), got.ExpiresAt)
	assert.Len(t, stored, 32, "SHA-256")
	assert.NotContains(t, string(stored), got.PageToken, "token asli tidak disimpan")

	again := newMocks(t)
	again.booking.EXPECT().GetPayable(gomock.Any(), bookingID).Return(payable(), nil)
	var second []byte
	again.tokens.EXPECT().Create(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).
		DoAndReturn(func(_ any, h []byte, _, _ string, _ time.Time) error { second = h; return nil })
	got2, _ := usecase.NewCreatePageToken(again.booking, again.tokens, clk, time.Minute).CreatePageToken(ctx(), payer, bookingID)
	assert.NotEqual(t, got.PageToken, got2.PageToken)
	assert.NotEqual(t, stored, second)
}

func TestCreatePageToken_RefusesNonPayableBooking(t *testing.T) {
	paid := payable()
	paid.Status = "PAID"
	m := newMocks(t)
	m.booking.EXPECT().GetPayable(gomock.Any(), bookingID).Return(paid, nil)
	_, err := usecase.NewCreatePageToken(m.booking, m.tokens, clk, time.Minute).CreatePageToken(ctx(), payer, bookingID)
	assert.Equal(t, inbound.CodeNotPayable, codeOf(t, err))
}

func TestExchangePageToken(t *testing.T) {
	t.Run("token valid → cookie", func(t *testing.T) {
		m := newMocks(t)
		m.tokens.EXPECT().Consume(gomock.Any(), gomock.Any(), now).Return(outbound.PageSession{BookingID: bookingID, VisitorID: visitorID}, nil)
		m.signer.EXPECT().Sign(visitorID, bookingID, now).Return("cookie-value", now.Add(15*time.Minute), nil)
		got, err := usecase.NewExchangePageToken(m.tokens, m.signer, clk).ExchangePageToken(ctx(), "token-yang-valid")
		require.NoError(t, err)
		assert.Equal(t, "cookie-value", got.Cookie)
		assert.Equal(t, now.Add(15*time.Minute), got.ExpiresAt)
	})
	t.Run("token dipakai ulang atau kedaluwarsa", func(t *testing.T) {
		m := newMocks(t)
		m.tokens.EXPECT().Consume(gomock.Any(), gomock.Any(), gomock.Any()).Return(outbound.PageSession{}, outbound.ErrPageTokenInvalid)
		_, err := usecase.NewExchangePageToken(m.tokens, m.signer, clk).ExchangePageToken(ctx(), "token")
		assert.Equal(t, inbound.CodePageTokenInvalid, codeOf(t, err))
	})
	t.Run("token kosong atau terlalu panjang tidak menyentuh store", func(t *testing.T) {
		m := newMocks(t)
		uc := usecase.NewExchangePageToken(m.tokens, m.signer, clk)
		for _, tok := range []string{"", string(make([]byte, 200))} {
			_, err := uc.ExchangePageToken(ctx(), tok)
			assert.Equal(t, inbound.CodePageTokenInvalid, codeOf(t, err))
		}
	})
	t.Run("store rusak → 503", func(t *testing.T) {
		m := newMocks(t)
		m.tokens.EXPECT().Consume(gomock.Any(), gomock.Any(), gomock.Any()).Return(outbound.PageSession{}, errors.New("db down"))
		_, err := usecase.NewExchangePageToken(m.tokens, m.signer, clk).ExchangePageToken(ctx(), "token")
		assert.Equal(t, apperror.CodeDependencyUnavailable, codeOf(t, err))
	})
}
