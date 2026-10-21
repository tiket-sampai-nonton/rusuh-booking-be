package inbound

import (
	"context"

	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/notification/internal/notification/domain/dto"
)

//go:generate go tool mockgen -source=notify_usecase.go -destination=../../../../_mock/notification/port/inbound/notify_usecase_mock.go -package=mock_inbound

// INotifyBookingPaid sends the e-ticket email for a booking.paid event.
type INotifyBookingPaid interface {
	NotifyBookingPaid(ctx context.Context, sourceMessageID string, p dto.BookingPaidPayload) error
}

// INotifyBookingExpired sends the expiry email for a booking.expired event.
type INotifyBookingExpired interface {
	NotifyBookingExpired(ctx context.Context, sourceMessageID string, p dto.BookingExpiredPayload) error
}

// INotifyBookingReview sends the review email for a booking.review event.
type INotifyBookingReview interface {
	NotifyBookingReview(ctx context.Context, sourceMessageID string, p dto.BookingReviewPayload) error
}

// INotifyTurnSoon sends the "turn soon" push for a queue.turn.soon event.
type INotifyTurnSoon interface {
	NotifyTurnSoon(ctx context.Context, sourceMessageID string, p dto.TurnSoonPayload) error
}

// INotifyTurnArrived sends the "turn arrived" push for a queue.turn.arrived event.
type INotifyTurnArrived interface {
	NotifyTurnArrived(ctx context.Context, sourceMessageID string, p dto.TurnArrivedPayload) error
}

// INotifyOTPRequested sends the OTP login email for an otp.requested event (no retry).
type INotifyOTPRequested interface {
	NotifyOTPRequested(ctx context.Context, sourceMessageID string, p dto.OTPRequestedPayload) error
}
