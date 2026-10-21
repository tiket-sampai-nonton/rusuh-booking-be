package usecase_test

import (
	"context"
	"encoding/base64"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	"github.com/tiket-sampai-nonton/rusuh-booking-be/pkg/clock"
	"github.com/tiket-sampai-nonton/rusuh-booking-be/pkg/rabbitmq"

	mock_outbound "github.com/tiket-sampai-nonton/rusuh-booking-be/services/notification/_mock/notification/port/outbound"
	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/notification/internal/notification/application/usecase"
	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/notification/internal/notification/domain/dto"
)

type otpMocks struct {
	tmpl  *mock_outbound.MockITemplateRenderer
	email *mock_outbound.MockIEmailSender
	pii   *mock_outbound.MockIPIIProtector
}

func newOTPUsecase(t *testing.T, now time.Time) (*usecase.NotifyOTPRequested, otpMocks) {
	ctrl := gomock.NewController(t)
	m := otpMocks{
		tmpl:  mock_outbound.NewMockITemplateRenderer(ctrl),
		email: mock_outbound.NewMockIEmailSender(ctrl),
		pii:   mock_outbound.NewMockIPIIProtector(ctrl),
	}
	return usecase.NewNotifyOTPRequested(m.tmpl, m.email, m.pii, clock.Fixed{T: now}, testLogger()), m
}

func otpPayload(expiresAt time.Time) dto.OTPRequestedPayload {
	return dto.OTPRequestedPayload{
		EmailEnc:  base64.RawURLEncoding.EncodeToString([]byte("e")),
		CodeEnc:   base64.RawURLEncoding.EncodeToString([]byte("c")),
		ExpiresAt: expiresAt.UTC().Format(time.RFC3339),
		Locale:    "id",
	}
}

func TestNotifyOTP_SendsEmail(t *testing.T) {
	now := time.Date(2026, 10, 20, 13, 45, 0, 0, time.UTC)
	uc, m := newOTPUsecase(t, now)

	m.pii.EXPECT().UnsealEmail(gomock.Any()).Return("buyer@example.id", nil)
	m.pii.EXPECT().UnsealOTP(gomock.Any()).Return("123456", nil)
	m.tmpl.EXPECT().Render("otp_login", gomock.Any()).Return("body", nil)
	m.email.EXPECT().SendEmail(gomock.Any(), "buyer@example.id", gomock.Any(), "body").Return("p", nil)

	require.NoError(t, uc.NotifyOTPRequested(context.Background(), "m1", otpPayload(now.Add(5*time.Minute))))
}

func TestNotifyOTP_ExpiredAcks(t *testing.T) {
	now := time.Date(2026, 10, 20, 14, 0, 0, 0, time.UTC)
	uc, _ := newOTPUsecase(t, now)
	// expiresAt in the past → ack, no decrypt/send (mocks would fail if called).
	require.NoError(t, uc.NotifyOTPRequested(context.Background(), "m1", otpPayload(now.Add(-time.Minute))))
}

func TestNotifyOTP_DecryptErrorPermanent(t *testing.T) {
	now := time.Date(2026, 10, 20, 13, 45, 0, 0, time.UTC)
	uc, m := newOTPUsecase(t, now)

	m.pii.EXPECT().UnsealEmail(gomock.Any()).Return("", errors.New("bad key"))

	err := uc.NotifyOTPRequested(context.Background(), "m1", otpPayload(now.Add(5*time.Minute)))
	require.Error(t, err)
	assert.True(t, rabbitmq.IsPermanent(err)) // TC-MSG-06
}

func TestNotifyOTP_SMTPErrorAcksNoRetry(t *testing.T) {
	now := time.Date(2026, 10, 20, 13, 45, 0, 0, time.UTC)
	uc, m := newOTPUsecase(t, now)

	m.pii.EXPECT().UnsealEmail(gomock.Any()).Return("buyer@example.id", nil)
	m.pii.EXPECT().UnsealOTP(gomock.Any()).Return("123456", nil)
	m.tmpl.EXPECT().Render("otp_login", gomock.Any()).Return("body", nil)
	m.email.EXPECT().SendEmail(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).Return("", errors.New("smtp down"))

	// SMTP failure is acked (time-sensitive, no retry).
	require.NoError(t, uc.NotifyOTPRequested(context.Background(), "m1", otpPayload(now.Add(5*time.Minute))))
}

func TestNotifyOTP_CorruptPayloadPermanent(t *testing.T) {
	uc, _ := newOTPUsecase(t, time.Now())
	err := uc.NotifyOTPRequested(context.Background(), "m1", dto.OTPRequestedPayload{})
	require.Error(t, err)
	assert.True(t, rabbitmq.IsPermanent(err))
}
