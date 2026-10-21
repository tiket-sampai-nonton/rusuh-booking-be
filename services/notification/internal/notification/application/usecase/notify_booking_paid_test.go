package usecase_test

import (
	"context"
	"errors"
	"io"
	"log/slog"
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

func testLogger() *slog.Logger { return slog.New(slog.NewTextHandler(io.Discard, nil)) }

func validPaidPayload() dto.BookingPaidPayload {
	return dto.BookingPaidPayload{
		BookingID:       "11111111-1111-1111-1111-111111111111",
		PublicRef:       "RBT-ABC123",
		BuyerEmail:      "buyer@example.id",
		Event:           dto.EventInfo{EventID: "e1", Name: "Konser ABC", StartsAt: "2026-11-15T19:00:00Z", Venue: "GBK"},
		Tickets:         []dto.TicketInfo{{TicketCode: "TKT-001", AttendeeName: "Budi", AttendeeNikMasked: "3174********0001", CategoryCode: "VIP"}},
		TicketLinkToken: "opaque-token",
	}
}

type paidMocks struct {
	repo  *mock_outbound.MockIDeliveryRepository
	tmpl  *mock_outbound.MockITemplateRenderer
	email *mock_outbound.MockIEmailSender
	pii   *mock_outbound.MockIPIIProtector
}

func newPaidUsecase(t *testing.T) (*usecase.NotifyBookingPaid, paidMocks) {
	t.Helper()
	ctrl := gomock.NewController(t)
	m := paidMocks{
		repo:  mock_outbound.NewMockIDeliveryRepository(ctrl),
		tmpl:  mock_outbound.NewMockITemplateRenderer(ctrl),
		email: mock_outbound.NewMockIEmailSender(ctrl),
		pii:   mock_outbound.NewMockIPIIProtector(ctrl),
	}
	uc := usecase.NewNotifyBookingPaid(m.repo, m.tmpl, m.email, m.pii, clock.Fixed{T: time.Now()}, testLogger())
	return uc, m
}

func TestNotifyBookingPaid_SendsAndMarksSent(t *testing.T) {
	uc, m := newPaidUsecase(t)
	m.pii.EXPECT().HMACRecipient("buyer@example.id").Return([]byte("h"))
	m.repo.EXPECT().ClaimDelivery(gomock.Any(), gomock.Any()).Return(true, nil)
	m.tmpl.EXPECT().Render("eticket", gomock.Any()).Return("body", nil)
	m.email.EXPECT().SendEmail(gomock.Any(), "buyer@example.id", gomock.Any(), "body").Return("prov-1", nil)
	m.repo.EXPECT().MarkSent(gomock.Any(), "msg-1", gomock.Any(), "prov-1").Return(nil)

	err := uc.NotifyBookingPaid(context.Background(), "msg-1", validPaidPayload())
	require.NoError(t, err)
}

func TestNotifyBookingPaid_DuplicateSkips(t *testing.T) {
	uc, m := newPaidUsecase(t)
	m.pii.EXPECT().HMACRecipient(gomock.Any()).Return([]byte("h"))
	m.repo.EXPECT().ClaimDelivery(gomock.Any(), gomock.Any()).Return(false, nil) // duplicate

	err := uc.NotifyBookingPaid(context.Background(), "msg-1", validPaidPayload())
	require.NoError(t, err) // ack, no email sent (email mock would fail if called)
}

func TestNotifyBookingPaid_CorruptPayloadPermanent(t *testing.T) {
	uc, _ := newPaidUsecase(t)
	err := uc.NotifyBookingPaid(context.Background(), "msg-1", dto.BookingPaidPayload{}) // missing fields
	require.Error(t, err)
	assert.True(t, rabbitmq.IsPermanent(err))
}

func TestNotifyBookingPaid_RenderErrorMarksDeadPermanent(t *testing.T) {
	uc, m := newPaidUsecase(t)
	m.pii.EXPECT().HMACRecipient(gomock.Any()).Return([]byte("h"))
	m.repo.EXPECT().ClaimDelivery(gomock.Any(), gomock.Any()).Return(true, nil)
	m.tmpl.EXPECT().Render("eticket", gomock.Any()).Return("", errors.New("boom"))
	m.repo.EXPECT().MarkDead(gomock.Any(), "msg-1", gomock.Any(), gomock.Any()).Return(nil)

	err := uc.NotifyBookingPaid(context.Background(), "msg-1", validPaidPayload())
	require.Error(t, err)
	assert.True(t, rabbitmq.IsPermanent(err))
}

func TestNotifyBookingPaid_SMTPErrorRetries(t *testing.T) {
	uc, m := newPaidUsecase(t)
	m.pii.EXPECT().HMACRecipient(gomock.Any()).Return([]byte("h"))
	m.repo.EXPECT().ClaimDelivery(gomock.Any(), gomock.Any()).Return(true, nil)
	m.tmpl.EXPECT().Render("eticket", gomock.Any()).Return("body", nil)
	m.email.EXPECT().SendEmail(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).Return("", errors.New("smtp down"))
	m.repo.EXPECT().BumpAttempt(gomock.Any(), "msg-1", gomock.Any(), gomock.Any(), gomock.Any()).Return(nil)

	err := uc.NotifyBookingPaid(context.Background(), "msg-1", validPaidPayload())
	require.Error(t, err)
	assert.False(t, rabbitmq.IsPermanent(err)) // transient → retry
}

func TestNotifyBookingPaid_ClaimErrorRetries(t *testing.T) {
	uc, m := newPaidUsecase(t)
	m.pii.EXPECT().HMACRecipient(gomock.Any()).Return([]byte("h"))
	m.repo.EXPECT().ClaimDelivery(gomock.Any(), gomock.Any()).Return(false, errors.New("db down"))

	err := uc.NotifyBookingPaid(context.Background(), "msg-1", validPaidPayload())
	require.Error(t, err)
	assert.False(t, rabbitmq.IsPermanent(err))
}
