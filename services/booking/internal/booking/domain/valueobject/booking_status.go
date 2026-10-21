package valueobject

// BookingStatus is the lifecycle state of a booking read model (matches bookings.status CHECK).
type BookingStatus string

// Booking statuses (migration 0001_init.up.sql bookings.status CHECK).
const (
	BookingPendingPayment BookingStatus = "PENDING_PAYMENT"
	BookingVerifying      BookingStatus = "VERIFYING"
	BookingPaid           BookingStatus = "PAID"
	BookingExpired        BookingStatus = "EXPIRED"
	BookingCancelled      BookingStatus = "CANCELLED"
	BookingReview         BookingStatus = "REVIEW"
)

// Valid reports whether s is a known booking status.
func (s BookingStatus) Valid() bool {
	switch s {
	case BookingPendingPayment, BookingVerifying, BookingPaid, BookingExpired, BookingCancelled, BookingReview:
		return true
	default:
		return false
	}
}

// IsTerminal reports whether no further transition is allowed.
func (s BookingStatus) IsTerminal() bool {
	switch s {
	case BookingPaid, BookingExpired, BookingCancelled:
		return true
	default:
		return false
	}
}

// IsPayable reports whether the booking still accepts settlement (held stock, not terminal).
func (s BookingStatus) IsPayable() bool {
	return s == BookingPendingPayment || s == BookingVerifying
}

// CanTransitionTo reports whether moving from s to next is allowed by the state machine (design §6.2).
func (s BookingStatus) CanTransitionTo(next BookingStatus) bool {
	switch s {
	case BookingPendingPayment:
		return next == BookingVerifying || next == BookingPaid || next == BookingExpired || next == BookingCancelled || next == BookingReview
	case BookingVerifying:
		return next == BookingPaid || next == BookingExpired || next == BookingReview
	default:
		return false
	}
}
