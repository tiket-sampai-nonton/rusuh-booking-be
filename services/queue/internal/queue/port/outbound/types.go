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
	NikMasked           string // "3174********0001"; from the login token claim when the account has a stored NIK
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
	AllocationID    string // allocation reserved at Booking (JTI of the admission token)
	ClaimDeadlineMs int64
}

// End outcomes originated by the Queue itself (Booking outcomes such as PAID/UNCLAIMED pass through unchanged).
const (
	EndSweepMissed    = "SWEEP_MISSED"          // Queue sweeper only: deadline passed
	EndQuotaExhaust   = "BUYER_QUOTA_EXHAUSTED" // promoter, WAITING entries only
	EndAlreadyActive  = "ALREADY_ACTIVE"        // promoter, WAITING entries only (Booking row is dead)
	EndPromoterOrphan = "PROMOTER_ORPHAN"       // promoter: id in waiting without an entry hash (C5)
	EndUnreadablePII  = "UNREADABLE_PII"        // promoter: WAITING entry whose email cannot be opened, entry-scoped (C6)
)

// EndResult is the typed reply of end_entry.lua.
type EndResult struct {
	Tag    string // OK | OK_CLEANUP | IGNORED
	Status string // final status for OK/OK_CLEANUP; current status (or NO_ENTRY) for IGNORED
}

// Changed reports whether the script mutated the entry.
func (r EndResult) Changed() bool { return r.Tag == "OK" || r.Tag == "OK_CLEANUP" }

// LeaveResult is the typed reply of leave.lua (NOT_FOUND is reported as entity.ErrNotFound).
type LeaveResult struct {
	Tag          string // OK | IGNORED
	PrevStatus   string // status before the script ran
	AllocationID string // set when PrevStatus was ADMITTED
}

// PromoteOpts tunes promote.lua.
type PromoteOpts struct {
	RequireHead bool   // the first candidate must be the head of waiting, otherwise nothing is promoted
	LeaseToken  string // mandatory: promoter lease token, the script is fenced by it
}

// PromoteSkip reports a candidate that promote.lua did not promote.
type PromoteSkip struct {
	EntryID      string
	Status       string // NO_ENTRY | LEFT | ENDED | COMPLETED | MISSED | ADMITTED_SAME | ADMITTED_OTHER | NOT_HEAD
	AllocationID string // stored allocation id for ADMITTED_OTHER
}

// PromoteResult is the typed reply of promote.lua.
type PromoteResult struct {
	Promoted  []string
	Skipped   []PromoteSkip
	Full      bool // no free slot: nothing promoted
	LeaseLost bool // the lease token no longer matches: nothing mutated
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
	BuyerEmailEnc  string `json:"buyerEmailEnc"` // AES-GCM, opened only by Notification
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
	NIKMasked string
	NIKHash   string
	Email     string
}
