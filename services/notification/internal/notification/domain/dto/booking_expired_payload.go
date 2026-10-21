package dto

import (
	"errors"
	"fmt"
)

// Expiry reasons (booking.expired.v1 enum).
const (
	ReasonPaymentTimeout = "PAYMENT_TIMEOUT"
	ReasonSessionExpired = "SESSION_EXPIRED"
	ReasonCancelled      = "CANCELLED"
)

// BookingExpiredPayload is the booking.expired.v1 payload (design §10.2).
type BookingExpiredPayload struct {
	BookingID  string `json:"bookingId"`
	PublicRef  string `json:"publicRef"`
	BuyerEmail string `json:"buyerEmail"`
	Reason     string `json:"reason"`
}

// Validate enforces the required fields of booking.expired.v1.
func (p BookingExpiredPayload) Validate() error {
	var errs []error
	if p.BookingID == "" {
		errs = append(errs, errors.New("bookingId kosong"))
	}
	if p.PublicRef == "" {
		errs = append(errs, errors.New("publicRef kosong"))
	}
	if p.BuyerEmail == "" {
		errs = append(errs, errors.New("buyerEmail kosong"))
	}
	switch p.Reason {
	case ReasonPaymentTimeout, ReasonSessionExpired, ReasonCancelled:
	default:
		errs = append(errs, fmt.Errorf("reason %q tidak dikenal", p.Reason))
	}
	if err := errors.Join(errs...); err != nil {
		return fmt.Errorf("booking.expired payload tidak valid: %w", err)
	}
	return nil
}
