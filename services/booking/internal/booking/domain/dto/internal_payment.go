package dto

import "time"

// Payable is the billing view of a booking returned to Payment Service.
type Payable struct {
	BookingID        string    `json:"bookingId"`
	PublicRef        string    `json:"publicRef"`
	Status           string    `json:"status"`
	AmountMinor      int64     `json:"amountMinor"`
	PaymentExpiresAt time.Time `json:"paymentExpiresAt"`
	VisitorID        string    `json:"visitorId"`
	Version          int       `json:"version"`
}

// PaymentLockRequest is the body of POST /internal/bookings/{id}/payment-lock.
type PaymentLockRequest struct {
	AttemptKey string `json:"attemptKey"`
}

// PaymentLockResponse is the outcome of payment-lock.
type PaymentLockResponse struct {
	Outcome          string    `json:"outcome"` // OK | BOOKING_NOT_PAYABLE | PAYMENT_EXPIRED
	Version          int       `json:"version,omitempty"`
	AmountMinor      int64     `json:"amountMinor,omitempty"`
	PaymentExpiresAt time.Time `json:"paymentExpiresAt,omitempty"`
}

// DeadlineExtensionRequest is the body of POST /internal/bookings/{id}/deadline-extension.
type DeadlineExtensionRequest struct {
	Reason  string `json:"reason"`
	Seconds int    `json:"seconds"`
}
