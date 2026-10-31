package dto

import (
	"errors"
	"fmt"
)

// BookingReviewPayload is the booking.review.v1 payload (design §10.3).
type BookingReviewPayload struct {
	BookingID     string `json:"bookingId"`
	AttemptHint   string `json:"attemptHint,omitempty"`
	BuyerEmail    string `json:"buyerEmail"`    // legacy plaintext: still accepted for messages produced before PDP-1
	BuyerEmailEnc string `json:"buyerEmailEnc"` // AES-GCM base64 (AAD booking.buyer_email); opened only when sending
	AmountMinor   int64  `json:"amountMinor"`
}

// Validate enforces the required fields of booking.review.v1.
func (p BookingReviewPayload) Validate() error {
	var errs []error
	if p.BookingID == "" {
		errs = append(errs, errors.New("bookingId kosong"))
	}
	if p.BuyerEmail == "" && p.BuyerEmailEnc == "" {
		errs = append(errs, errors.New("buyerEmailEnc kosong"))
	}
	if p.AmountMinor < 0 {
		errs = append(errs, errors.New("amountMinor < 0"))
	}
	if err := errors.Join(errs...); err != nil {
		return fmt.Errorf("booking.review payload tidak valid: %w", err)
	}
	return nil
}
