package dto

import (
	"errors"
	"fmt"
)

// BookingReviewPayload is the booking.review.v1 payload (design §10.3).
type BookingReviewPayload struct {
	BookingID   string `json:"bookingId"`
	AttemptHint string `json:"attemptHint,omitempty"`
	BuyerEmail  string `json:"buyerEmail"`
	AmountMinor int64  `json:"amountMinor"`
}

// Validate enforces the required fields of booking.review.v1.
func (p BookingReviewPayload) Validate() error {
	var errs []error
	if p.BookingID == "" {
		errs = append(errs, errors.New("bookingId kosong"))
	}
	if p.BuyerEmail == "" {
		errs = append(errs, errors.New("buyerEmail kosong"))
	}
	if p.AmountMinor < 0 {
		errs = append(errs, errors.New("amountMinor < 0"))
	}
	if err := errors.Join(errs...); err != nil {
		return fmt.Errorf("booking.review payload tidak valid: %w", err)
	}
	return nil
}
