package valueobject

// AdmissionStatus is the lifecycle state of an admission (matches admissions.status CHECK).
type AdmissionStatus string

// Admission statuses (migration 0001_init.up.sql admissions.status CHECK; design §6.1).
const (
	AdmissionReserved  AdmissionStatus = "RESERVED"
	AdmissionActive    AdmissionStatus = "ACTIVE"
	AdmissionEditable  AdmissionStatus = "EDITABLE"
	AdmissionConverted AdmissionStatus = "CONVERTED"
	AdmissionExpired   AdmissionStatus = "EXPIRED"
	AdmissionReleased  AdmissionStatus = "RELEASED"
)

// Valid reports whether s is a known admission status.
func (s AdmissionStatus) Valid() bool {
	switch s {
	case AdmissionReserved, AdmissionActive, AdmissionEditable, AdmissionConverted, AdmissionExpired, AdmissionReleased:
		return true
	default:
		return false
	}
}

// IsActive reports whether the admission is still live (ended_at IS NULL per DB CHECK).
func (s AdmissionStatus) IsActive() bool {
	return s == AdmissionReserved || s == AdmissionActive || s == AdmissionEditable
}

// LockReason explains why an admission was converted (matches admissions.lock_reason CHECK).
type LockReason string

// Lock reasons.
const (
	LockPaymentStarted   LockReason = "PAYMENT_STARTED"
	LockSelectionExpired LockReason = "SELECTION_EXPIRED"
	LockEditDisabled     LockReason = "EDIT_DISABLED"
	LockBookingEnded     LockReason = "BOOKING_ENDED"
)

// EndReason explains why an admission ended (matches admissions.end_reason CHECK).
type EndReason string

// End reasons.
const (
	EndUnclaimed         EndReason = "UNCLAIMED"
	EndSessionExpired    EndReason = "SESSION_EXPIRED"
	EndLeftWithoutBuying EndReason = "LEFT_WITHOUT_BUYING"
	EndConverted         EndReason = "CONVERTED"
)
