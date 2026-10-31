// Package dto holds the event payload and template data structures of the Notification domain
// (standard library only). Field tags match the docs/contracts/events schemas.
package dto

import (
	"errors"
	"fmt"
)

// EventInfo is the nested event block in booking.paid.v1.
type EventInfo struct {
	EventID  string `json:"eventId"`
	Name     string `json:"name"`
	StartsAt string `json:"startsAt"`
	Venue    string `json:"venue"`
}

// TicketInfo is one ticket line in booking.paid.v1.
type TicketInfo struct {
	TicketCode        string `json:"ticketCode"`
	AttendeeName      string `json:"attendeeName"`
	AttendeeNikMasked string `json:"attendeeNikMasked"`
	CategoryCode      string `json:"categoryCode"`
}

// BookingPaidPayload is the booking.paid.v1 payload (design §10.1).
type BookingPaidPayload struct {
	BookingID       string       `json:"bookingId"`
	PublicRef       string       `json:"publicRef"`
	BuyerEmail      string       `json:"buyerEmail"`    // legacy plaintext: still accepted for messages produced before PDP-1
	BuyerEmailEnc   string       `json:"buyerEmailEnc"` // AES-GCM base64 (AAD booking.buyer_email); opened only when sending
	Event           EventInfo    `json:"event"`
	Tickets         []TicketInfo `json:"tickets"`
	TicketLinkToken string       `json:"ticketLinkToken"`
}

// Validate enforces the required fields of booking.paid.v1. A failure is a poison payload.
// ticketLinkToken is optional: Booking does not issue it yet and the email then omits the link line.
func (p BookingPaidPayload) Validate() error {
	var errs []error
	if p.BookingID == "" {
		errs = append(errs, errors.New("bookingId kosong"))
	}
	if p.PublicRef == "" {
		errs = append(errs, errors.New("publicRef kosong"))
	}
	if p.BuyerEmail == "" && p.BuyerEmailEnc == "" {
		errs = append(errs, errors.New("buyerEmailEnc kosong"))
	}
	if len(p.Tickets) == 0 {
		errs = append(errs, errors.New("tickets kosong"))
	}
	if err := errors.Join(errs...); err != nil {
		return fmt.Errorf("booking.paid payload tidak valid: %w", err)
	}
	return nil
}
