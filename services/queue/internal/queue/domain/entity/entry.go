package entity

import (
	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/queue/internal/queue/domain/valueobject"
)

// Entry is one queue entry (state held in Valkey hash q:{e}:entry:<id>, design §3.2).
type Entry struct {
	ID        string
	VisitorID string
	AccountID string // "" for guest (ADR-020)
	Seq       valueobject.Seq
	// Seq is the admission order (ADR-030 Amendment: the opening-window draw was dropped, so all joins are strict
	// FIFO by seq).
	LastSignalMs    int64  // join time or latest valid heartbeat
	EndReason       string // raw outcome that ended the entry (LEFT, STALE_TIMEOUT, SWEEP_MISSED, Booking outcome)
	Status          valueobject.Status
	AllocationID    string
	JoinedAtMs      int64
	AdmittedAtMs    int64
	ClaimedAtMs     int64
	ClaimDeadlineMs int64
}

// IsTerminal reports whether the entry is in a terminal status.
func (e Entry) IsTerminal() bool { return e.Status.IsTerminal() }
