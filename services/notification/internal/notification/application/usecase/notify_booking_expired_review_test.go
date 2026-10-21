package usecase_test

import (
	"context"
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

func TestNotifyBookingExpired_SendsAndMarksSent(t *testing.T) {
	ctrl := gomock.NewController(t)
	repo := mock_outbound.NewMockIDeliveryRepository(ctrl)
	tmpl := mock_outbound.NewMockITemplateRenderer(ctrl)
	email := mock_outbound.NewMockIEmailSender(ctrl)
	pii := mock_outbound.NewMockIPIIProtector(ctrl)
	uc := usecase.NewNotifyBookingExpired(repo, tmpl, email, pii, clock.Fixed{T: time.Now()}, testLogger())

	pii.EXPECT().HMACRecipient(gomock.Any()).Return([]byte("h"))
	repo.EXPECT().ClaimDelivery(gomock.Any(), gomock.Any()).Return(true, nil)
	tmpl.EXPECT().Render("expired", gomock.Any()).Return("body", nil)
	email.EXPECT().SendEmail(gomock.Any(), "buyer@example.id", gomock.Any(), "body").Return("p", nil)
	repo.EXPECT().MarkSent(gomock.Any(), "m1", gomock.Any(), "p").Return(nil)

	p := dto.BookingExpiredPayload{BookingID: "b1", PublicRef: "RBT-1", BuyerEmail: "buyer@example.id", Reason: dto.ReasonPaymentTimeout}
	require.NoError(t, uc.NotifyBookingExpired(context.Background(), "m1", p))
}

func TestNotifyBookingExpired_BadReasonPermanent(t *testing.T) {
	ctrl := gomock.NewController(t)
	uc := usecase.NewNotifyBookingExpired(
		mock_outbound.NewMockIDeliveryRepository(ctrl),
		mock_outbound.NewMockITemplateRenderer(ctrl),
		mock_outbound.NewMockIEmailSender(ctrl),
		mock_outbound.NewMockIPIIProtector(ctrl),
		clock.Fixed{T: time.Now()}, testLogger())

	p := dto.BookingExpiredPayload{BookingID: "b1", PublicRef: "RBT-1", BuyerEmail: "x@y.id", Reason: "NOPE"}
	err := uc.NotifyBookingExpired(context.Background(), "m1", p)
	require.Error(t, err)
	assert.True(t, rabbitmq.IsPermanent(err))
}

func TestNotifyBookingReview_SendsAndMarksSent(t *testing.T) {
	ctrl := gomock.NewController(t)
	repo := mock_outbound.NewMockIDeliveryRepository(ctrl)
	tmpl := mock_outbound.NewMockITemplateRenderer(ctrl)
	email := mock_outbound.NewMockIEmailSender(ctrl)
	pii := mock_outbound.NewMockIPIIProtector(ctrl)
	uc := usecase.NewNotifyBookingReview(repo, tmpl, email, pii, clock.Fixed{T: time.Now()}, testLogger())

	pii.EXPECT().HMACRecipient(gomock.Any()).Return([]byte("h"))
	repo.EXPECT().ClaimDelivery(gomock.Any(), gomock.Any()).Return(true, nil)
	tmpl.EXPECT().Render("review", gomock.Any()).Return("body", nil)
	email.EXPECT().SendEmail(gomock.Any(), "buyer@example.id", gomock.Any(), "body").Return("p", nil)
	repo.EXPECT().MarkSent(gomock.Any(), "m1", gomock.Any(), "p").Return(nil)

	p := dto.BookingReviewPayload{BookingID: "b1", BuyerEmail: "buyer@example.id", AmountMinor: 3000000}
	require.NoError(t, uc.NotifyBookingReview(context.Background(), "m1", p))
}

func TestNotifyBookingReview_DuplicateSkips(t *testing.T) {
	ctrl := gomock.NewController(t)
	repo := mock_outbound.NewMockIDeliveryRepository(ctrl)
	pii := mock_outbound.NewMockIPIIProtector(ctrl)
	uc := usecase.NewNotifyBookingReview(repo,
		mock_outbound.NewMockITemplateRenderer(ctrl),
		mock_outbound.NewMockIEmailSender(ctrl), pii,
		clock.Fixed{T: time.Now()}, testLogger())

	pii.EXPECT().HMACRecipient(gomock.Any()).Return([]byte("h"))
	repo.EXPECT().ClaimDelivery(gomock.Any(), gomock.Any()).Return(false, nil)

	p := dto.BookingReviewPayload{BookingID: "b1", BuyerEmail: "x@y.id", AmountMinor: 1}
	require.NoError(t, uc.NotifyBookingReview(context.Background(), "m1", p))
}
