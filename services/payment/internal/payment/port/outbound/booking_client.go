package outbound

import (
	"context"
	"errors"
	"time"
)

//go:generate go tool mockgen -source=booking_client.go -destination=../../../../_mock/payment/port/outbound/booking_client_mock.go -package=mock_outbound

// Booking client errors. ErrBookingNotFound maps to 404; ErrBookingUnavailable to 503.
var (
	ErrBookingNotFound    = errors.New("booking: tidak ditemukan")
	ErrBookingUnavailable = errors.New("booking: tidak terjangkau")
)

// Payable is internal-booking.yaml Payable.
type Payable struct {
	BookingID        string
	PublicRef        string
	Status           string
	AmountMinor      int64
	PaymentExpiresAt time.Time
	VisitorID        string
	Version          int
	CanEdit          bool
}

// Payment-lock outcomes (internal-booking.yaml PaymentLockResponse).
const (
	LockOK                = "OK"
	LockBookingNotPayable = "BOOKING_NOT_PAYABLE"
	LockPaymentExpired    = "PAYMENT_EXPIRED"
)

// PaymentLock is internal-booking.yaml PaymentLockResponse. On OK, AmountMinor is the locked amount to charge.
type PaymentLock struct {
	Outcome          string
	Version          int
	AmountMinor      int64
	PaymentExpiresAt time.Time
}

// IBookingClient calls Booking's internal API with a service token and a breaker.
type IBookingClient interface {
	GetPayable(ctx context.Context, bookingID string) (Payable, error)
	Lock(ctx context.Context, bookingID, attemptKey string) (PaymentLock, error)
}
