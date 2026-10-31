package entity

import (
	"encoding/hex"

	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/queue/internal/queue/domain/valueobject"
)

// Entry is one queue entry (state held in Valkey hash q:{e}:entry:<id>, design §3.2).
type Entry struct {
	ID              string
	VisitorID       string
	AccountID       string // "" for guest (ADR-020)
	Seq             valueobject.Seq
	BuyerNikHash    []byte
	BuyerNikMasked  string
	EmailEnc        []byte
	EmailHash       []byte // HMAC of email, hex-stored; used to rebuild the email constraint key
	Status          valueobject.Status
	AllocationID    string
	JoinedAtMs      int64
	AdmittedAtMs    int64
	ClaimedAtMs     int64
	ClaimDeadlineMs int64
}

// ExactPosition returns how many entries are ahead: seq - servingSeq - removedAhead (design §2.3).
func (e Entry) ExactPosition(servingSeq, removedAhead int64) int64 {
	return e.Seq.Int64() - servingSeq - removedAhead
}

// BuyerNikHashHex returns the lowercase hex of the NIK hash.
func (e Entry) BuyerNikHashHex() string { return hex.EncodeToString(e.BuyerNikHash) }

// EmailHashHex returns the lowercase hex of the email hash.
func (e Entry) EmailHashHex() string { return hex.EncodeToString(e.EmailHash) }

// IsTerminal reports whether the entry is in a terminal status.
func (e Entry) IsTerminal() bool { return e.Status.IsTerminal() }
