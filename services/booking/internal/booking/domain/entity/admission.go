package entity

import (
	"time"

	vo "github.com/tiket-sampai-nonton/rusuh-booking-be/services/booking/internal/booking/domain/valueobject"
)

// Admission is an allocation + selection session (admissions table). It carries a config snapshot taken
// when the turn was granted, so live event-config changes never affect a running session (invariant #4).
type Admission struct {
	ID             string
	EventID        string
	QueueEntryID   string
	QueueSeq       int64
	VisitorID      string
	BuyerNIKHash   []byte
	BuyerEmail     string
	BuyerNIKMasked string
	Units          int
	Status         vo.AdmissionStatus
	AdmissionJTI   string
	AttendeeChecks int
	EditCount      int
	AccountID      string // empty when guest

	// Config snapshot (cfg_* columns).
	CfgSelectionTTLSeconds  int
	CfgPaymentTTLSeconds    int
	CfgSubmitGraceSeconds   int
	CfgEditEnabled          bool
	CfgMaxEdits             int
	CfgAllowMixedCategories bool

	ReservedAt time.Time
	ClaimedAt  *time.Time
	ExpiresAt  time.Time
	LockedAt   *time.Time
	LockReason *vo.LockReason
	EndedAt    *time.Time
	EndReason  *vo.EndReason
}

// IsActive reports whether the admission is still live.
func (a Admission) IsActive() bool { return a.Status.IsActive() }

// IsExpired reports whether now is past the admission's expiry.
func (a Admission) IsExpired(now time.Time) bool { return !now.Before(a.ExpiresAt) }

// ChecksRemaining returns how many more unique attendee checks the session allows.
func (a Admission) ChecksRemaining(maxChecks int) int {
	r := maxChecks - a.AttendeeChecks
	if r < 0 {
		return 0
	}
	return r
}

// EditsRemaining returns how many more edits the session allows.
func (a Admission) EditsRemaining() int {
	r := a.CfgMaxEdits - a.EditCount
	if r < 0 {
		return 0
	}
	return r
}

// CanEdit reports whether editing is still permitted (enabled, under the limit, still active).
func (a Admission) CanEdit() bool {
	return a.CfgEditEnabled && a.IsActive() && a.EditsRemaining() > 0
}
