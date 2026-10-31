package entity

import (
	"time"

	vo "github.com/tiket-sampai-nonton/rusuh-booking-be/services/booking/internal/booking/domain/valueobject"
)

// Booking is the read-model projection of the Booking aggregate (bookings table). The authoritative
// history is the booking_events stream; this struct is rebuilt from it in the same transaction.
type Booking struct {
	ID           string
	PublicRef    string
	EventID      string
	AdmissionID  string
	VisitorID    string
	BuyerNIKHash []byte
	// BuyerNIKMasked comes from the admission; plaintext NIK is never exposed.
	BuyerNIKMasked string
	BuyerEmailEnc  string
	Status         vo.BookingStatus
	Currency       string
	AmountMinor    int64
	ItemQuantity   int
	Version        vo.Version
	AccountID      string // empty when guest

	CreatedAt          time.Time
	PaymentExpiresAt   time.Time
	PaymentStartedAt   *time.Time
	DeadlineExtendedAt *time.Time
	VerificationUntil  *time.Time
	SettlementTime     *time.Time
	PaidAt             *time.Time
	EndedAt            *time.Time

	Items []BookingItem
}

// IsLocked reports whether payment has started, which forbids edits and cancels.
func (b Booking) IsLocked() bool { return b.PaymentStartedAt != nil }

// CanEdit reports whether the booking may still be edited (not locked, not terminal).
func (b Booking) CanEdit() bool {
	return !b.IsLocked() && b.Status == vo.BookingPendingPayment
}

// IsExpired reports whether the payment window has elapsed at now.
func (b Booking) IsExpired(now time.Time) bool { return !now.Before(b.PaymentExpiresAt) }

// BookingItem is one ticket line (booking_items table); one row per attendee.
type BookingItem struct {
	ID                string
	BookingID         string
	EventID           string
	CategoryID        string
	UnitPriceMinor    int64
	AttendeeNIKHash   []byte
	AttendeeNIKEnc    []byte
	AttendeeNIKMasked string
	AttendeeName      string
	Status            vo.ItemStatus
}

// Ticket is an issued e-ticket (tickets table); 1:1 with a paid booking item.
type Ticket struct {
	ID            string
	BookingID     string
	BookingItemID string
	PublicCode    string
	IssuedAt      time.Time

	// Enriched display fields (joined from booking_items for the TicketList DTO).
	CategoryCode      string
	AttendeeName      string
	AttendeeNIKMasked string
}
