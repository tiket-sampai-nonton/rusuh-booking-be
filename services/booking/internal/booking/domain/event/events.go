// Package event defines the Booking aggregate's domain events. These are the payloads appended to the
// booking_events stream (pkg/eventstore). They carry NO plaintext NIK/email/token — only hashes and
// masked forms — because pkg/eventstore rejects forbidden fields (ErrForbiddenField).
package event

// Type names (must match ^[A-Z][A-Za-z]+$ enforced by the event store and the DB CHECK).
const (
	TypeBookingHeld          = "BookingHeld"
	TypeBookingItemsReplaced = "BookingItemsReplaced"
	TypeBookingLocked        = "BookingLocked"
	TypeBookingPaid          = "BookingPaid"
	TypeBookingExpired       = "BookingExpired"
	TypeBookingReview        = "BookingReview"
)

// ItemSnapshot describes one held ticket line without any PII plaintext.
type ItemSnapshot struct {
	CategoryID        string `json:"categoryId"`
	UnitPriceMinor    int64  `json:"unitPriceMinor"`
	AttendeeNIKMasked string `json:"attendeeNikMasked"`
}

// BookingHeld is appended at version 1 when a hold succeeds.
type BookingHeld struct {
	BookingID    string         `json:"bookingId"`
	EventID      string         `json:"eventId"`
	AdmissionID  string         `json:"admissionId"`
	AmountMinor  int64          `json:"amountMinor"`
	ItemQuantity int            `json:"itemQuantity"`
	Items        []ItemSnapshot `json:"items"`
}

// BookingItemsReplaced is appended when a buyer edits the order before paying.
type BookingItemsReplaced struct {
	BookingID    string         `json:"bookingId"`
	AmountMinor  int64          `json:"amountMinor"`
	ItemQuantity int            `json:"itemQuantity"`
	Items        []ItemSnapshot `json:"items"`
}

// BookingLocked is appended when payment starts (order becomes immutable).
type BookingLocked struct {
	BookingID string `json:"bookingId"`
	Reason    string `json:"reason"`
}

// BookingPaid is appended on settlement.
type BookingPaid struct {
	BookingID   string `json:"bookingId"`
	AmountMinor int64  `json:"amountMinor"`
	Method      string `json:"method"`
}

// BookingExpired is appended when the payment window lapses with no payment.
type BookingExpired struct {
	BookingID string `json:"bookingId"`
	Reason    string `json:"reason"` // PAYMENT_TIMEOUT | SESSION_EXPIRED | CANCELLED
}

// BookingReview is appended when expiry resolve finds a payment at the gateway.
type BookingReview struct {
	BookingID   string `json:"bookingId"`
	AmountMinor int64  `json:"amountMinor"`
}
