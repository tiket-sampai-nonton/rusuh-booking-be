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
	"github.com/tiket-sampai-nonton/rusuh-booking-be/pkg/rabbitmq"

	mock_outbound "github.com/tiket-sampai-nonton/rusuh-booking-be/services/notification/_mock/notification/port/outbound"
	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/notification/internal/notification/application/usecase"
	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/notification/internal/notification/domain/dto"
)

// A provider rejection (rabbitmq.Permanent, e.g. Resend 403) must end as DEAD, matching the DLQ;
// a transient failure keeps the delivery in RETRY. The idempotency key is source message id + type.

func TestNotifyBookingPaid_PassesIdempotencyKey(t *testing.T) {
	uc, m := newPaidUsecase(t)
	m.pii.EXPECT().HMACRecipient(gomock.Any()).Return([]byte("h"))
	m.repo.EXPECT().ClaimDelivery(gomock.Any(), gomock.Any()).Return(true, nil)
	m.tmpl.EXPECT().Render("eticket", gomock.Any()).Return("body", nil)
	m.tmpl.EXPECT().RenderHTML("eticket", gomock.Any()).Return("<p>html</p>", nil)
	m.email.EXPECT().SendEmail(gomock.Any(), gomock.Any(), gomock.Any(), "body", gomock.Any(), "msg-1:BOOKING_PAID").Return("p", nil)
	m.repo.EXPECT().MarkSent(gomock.Any(), "msg-1", gomock.Any(), "p").Return(nil)

	require.NoError(t, uc.NotifyBookingPaid(context.Background(), "msg-1", validPaidPayload()))
}

func TestNotifyBookingPaid_PermanentSendErrorMarksDead(t *testing.T) {
	uc, m := newPaidUsecase(t)
	m.pii.EXPECT().HMACRecipient(gomock.Any()).Return([]byte("h"))
	m.repo.EXPECT().ClaimDelivery(gomock.Any(), gomock.Any()).Return(true, nil)
	m.tmpl.EXPECT().Render("eticket", gomock.Any()).Return("body", nil)
	m.tmpl.EXPECT().RenderHTML("eticket", gomock.Any()).Return("<p>html</p>", nil)
	m.email.EXPECT().SendEmail(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).
		Return("", rabbitmq.Permanent(errors.New("resend: status 403 validation_error")))
	m.repo.EXPECT().MarkDead(gomock.Any(), "msg-1", gomock.Any(), gomock.Any()).Return(nil) // no BumpAttempt expected

	err := uc.NotifyBookingPaid(context.Background(), "msg-1", validPaidPayload())
	require.Error(t, err)
	assert.True(t, rabbitmq.IsPermanent(err))
}

func TestNotifyBookingExpired_SendErrors(t *testing.T) {
	tests := []struct {
		name      string
		sendErr   error
		permanent bool
	}{
		{"permanent marks dead", rabbitmq.Permanent(errors.New("resend: status 422")), true},
		{"transient bumps attempt", errors.New("resend: status 429"), false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctrl := gomock.NewController(t)
			repo := mock_outbound.NewMockIDeliveryRepository(ctrl)
			tmpl := mock_outbound.NewMockITemplateRenderer(ctrl)
			email := mock_outbound.NewMockIEmailSender(ctrl)
			pii := mock_outbound.NewMockIPIIProtector(ctrl)
			uc := usecase.NewNotifyBookingExpired(repo, tmpl, email, pii, clock.Fixed{T: time.Now()}, testLogger())

			pii.EXPECT().HMACRecipient(gomock.Any()).Return([]byte("h"))
			repo.EXPECT().ClaimDelivery(gomock.Any(), gomock.Any()).Return(true, nil)
			tmpl.EXPECT().Render("expired", gomock.Any()).Return("body", nil)
			email.EXPECT().SendEmail(gomock.Any(), gomock.Any(), gomock.Any(), "body", gomock.Any(), "m1:BOOKING_EXPIRED").Return("", tt.sendErr)
			if tt.permanent {
				repo.EXPECT().MarkDead(gomock.Any(), "m1", gomock.Any(), gomock.Any()).Return(nil)
			} else {
				repo.EXPECT().BumpAttempt(gomock.Any(), "m1", gomock.Any(), gomock.Any(), gomock.Any()).Return(nil)
			}

			p := dto.BookingExpiredPayload{BookingID: "b1", PublicRef: "RBT-1", BuyerEmail: "buyer@example.id", Reason: dto.ReasonPaymentTimeout}
			err := uc.NotifyBookingExpired(context.Background(), "m1", p)
			require.Error(t, err)
			assert.Equal(t, tt.permanent, rabbitmq.IsPermanent(err))
		})
	}
}

func TestNotifyBookingReview_SendErrors(t *testing.T) {
	tests := []struct {
		name      string
		sendErr   error
		permanent bool
	}{
		{"permanent marks dead", rabbitmq.Permanent(errors.New("resend: status 401")), true},
		{"transient bumps attempt", errors.New("resend: status 500"), false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctrl := gomock.NewController(t)
			repo := mock_outbound.NewMockIDeliveryRepository(ctrl)
			tmpl := mock_outbound.NewMockITemplateRenderer(ctrl)
			email := mock_outbound.NewMockIEmailSender(ctrl)
			pii := mock_outbound.NewMockIPIIProtector(ctrl)
			uc := usecase.NewNotifyBookingReview(repo, tmpl, email, pii, clock.Fixed{T: time.Now()}, testLogger())

			pii.EXPECT().HMACRecipient(gomock.Any()).Return([]byte("h"))
			repo.EXPECT().ClaimDelivery(gomock.Any(), gomock.Any()).Return(true, nil)
			tmpl.EXPECT().Render("review", gomock.Any()).Return("body", nil)
			email.EXPECT().SendEmail(gomock.Any(), gomock.Any(), gomock.Any(), "body", gomock.Any(), "m1:BOOKING_REVIEW").Return("", tt.sendErr)
			if tt.permanent {
				repo.EXPECT().MarkDead(gomock.Any(), "m1", gomock.Any(), gomock.Any()).Return(nil)
			} else {
				repo.EXPECT().BumpAttempt(gomock.Any(), "m1", gomock.Any(), gomock.Any(), gomock.Any()).Return(nil)
			}

			p := dto.BookingReviewPayload{BookingID: "b1", BuyerEmail: "buyer@example.id", AmountMinor: 3000000}
			err := uc.NotifyBookingReview(context.Background(), "m1", p)
			require.Error(t, err)
			assert.Equal(t, tt.permanent, rabbitmq.IsPermanent(err))
		})
	}
}

func TestNotifyOTP_PassesIdempotencyKey(t *testing.T) {
	now := time.Date(2026, 10, 20, 13, 45, 0, 0, time.UTC)
	uc, m := newOTPUsecase(t, now)
	m.pii.EXPECT().UnsealEmail(gomock.Any()).Return("buyer@example.id", nil)
	m.pii.EXPECT().UnsealOTP(gomock.Any()).Return("123456", nil)
	m.tmpl.EXPECT().Render("otp_login", gomock.Any()).Return("body", nil)
	m.email.EXPECT().SendEmail(gomock.Any(), gomock.Any(), gomock.Any(), "body", gomock.Any(), "m1:OTP_LOGIN").Return("p", nil)

	require.NoError(t, uc.NotifyOTPRequested(context.Background(), "m1", otpPayload(now.Add(5*time.Minute))))
}
