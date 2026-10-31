package outbound

import "time"

// JoinParams carries everything join.lua needs.
type JoinParams struct {
	EventID             string
	EntryID             string
	VisitorID           string
	NikHash             string // hex
	EmailHashHex        string // hex HMAC of email; used for the email constraint key
	EmailEnc            []byte
	NikMasked           string // "3174********0001"; empty when the NIK is not in the request (logged-in with stored NIK)
	AccountID           string
	NowMs               int64
	OneActivePerVisitor bool
	OneActivePerEmail   bool
}

// EmailHash returns the hex email hash (used to build the email constraint key).
func (p JoinParams) EmailHash() string { return p.EmailHashHex }

// JoinOutcome enumerates the join.lua result kinds.
type JoinOutcome string

// Join outcomes.
const (
	JoinOK             JoinOutcome = "OK"
	JoinExistingNIK    JoinOutcome = "EXISTING_NIK"
	JoinExistingVisit  JoinOutcome = "EXISTING_VISITOR"
	JoinExistingEmail  JoinOutcome = "EXISTING_EMAIL"
	JoinQuotaExhausted JoinOutcome = "QUOTA_EXHAUSTED"
)

// JoinResult is the mapped outcome of join.lua.
type JoinResult struct {
	Outcome JoinOutcome
	Seq     int64
	// ExistingEntryID is set when Outcome is one of the EXISTING_* kinds.
	ExistingEntryID string
}

// PromoteCandidate is one reserved entry to move waiting→active.
type PromoteCandidate struct {
	EntryID         string
	AllocationID    string
	ClaimDeadlineMs int64
}

// ClaimResult is the mapped outcome of claim.lua.
type ClaimResult struct {
	Status          string // CLAIMED | ALREADY
	ClaimDeadlineMs int64
}

// SaleState is the q:{e}:sale hash.
type SaleState struct {
	SaleState  string // NOT_STARTED | OPEN | CLOSED
	SoldOut    bool
	ServingSeq int64
}

// AuditRecord is one entry_audit row.
type AuditRecord struct {
	EventID      string
	EntryID      string
	Seq          int64
	BuyerNikHash []byte
	Action       string
	OccurredAt   time.Time
	Metadata     map[string]any
	AccountID    string // "" for guest
}

// AllocationBatchRequest is POST /internal/v1/allocations:batch.
type AllocationBatchRequest struct {
	EventID string                   `json:"eventId"`
	Entries []AllocationEntryRequest `json:"entries"`
}

// AllocationEntryRequest is one entry in the batch.
type AllocationEntryRequest struct {
	QueueEntryID string `json:"queueEntryId"`
	Seq          int64  `json:"seq"`
	VisitorID    string `json:"visitorId"`
	BuyerNikHash string `json:"buyerNikHash"`
	Units        int    `json:"units"`
	// Contract internal-booking AllocationBatchRequest: Booking keeps these on the admission and the booking
	// (e-ticket email, buyer NIK shown masked, login account).
	BuyerNikMasked string `json:"buyerNikMasked"`
	BuyerEmail     string `json:"buyerEmail"`
	AccountID      string `json:"accountId,omitempty"`
}

// AllocationBatchResponse is the batch reply.
type AllocationBatchResponse struct {
	Allocations []AllocationResult `json:"results"`
}

// AllocationResult is one allocation outcome.
type AllocationResult struct {
	QueueEntryID  string `json:"queueEntryId"`
	AllocationID  string `json:"allocationId"`
	Status        string `json:"outcome"` // GRANTED | NO_SUPPLY_NOW | SOLD_OUT | QUOTA_EXHAUSTED
	Units         int    `json:"units"`
	ClaimDeadline string `json:"claimDeadline"`
}

// SupplySnapshot is GET /internal/v1/events/{id}/supply.
type SupplySnapshot struct {
	EventID          string           `json:"eventId"`
	RemainingTickets int              `json:"remainingTickets"`
	SoldOut          bool             `json:"soldOut"`
	Categories       []SupplyCategory `json:"categories"`
}

// SupplyCategory is one ticket category's stock.
type SupplyCategory struct {
	Code  string `json:"code"`
	Name  string `json:"name"`
	Sold  int    `json:"sold"`
	Total int    `json:"total"`
}

// UserIdentity is the resolved buyer identity from a User Service bearer token.
type UserIdentity struct {
	AccountID string
	HasNIK    bool
	NIKHash   string
	Email     string
}
