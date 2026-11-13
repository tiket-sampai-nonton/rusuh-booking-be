package outbound

import "time"

// JoinParams carries everything join.lua needs. There is no NIK, email or consent: the join ranks an anonymous
// principal (ADR-030). The bearer account id identifies the principal only.
type JoinParams struct {
	EventID             string
	EntryID             string // id for a new entry
	NewVisitorID        string // visitor id minted by the caller, used when no verified token supplies one
	TokenVisitorID      string // visitor id of a verified visitor token ("" = none)
	AccountID           string // verified bearer account ("" = none)
	IdemKey             string // optional Idempotency-Key
	NowMs               int64
	IdemTTL             time.Duration
	OneActivePerVisitor bool
	// TokenGen is the generation carried by the verified visitor token (0 = none/legacy). A token whose generation is
	// not the server's current one is ignored (rotated out).
	TokenGen int64
	// TokenTTL is the lifetime of the server-side token record (vtok), equal to the visitor token's lifetime.
	TokenTTL time.Duration
	// GuestScope scopes IdemKey for a tokenless guest (device hash or keyed client IP, C6.2). "" = the key is ignored.
	GuestScope string
}

// JoinOutcome enumerates the join.lua result kinds.
type JoinOutcome string

// Join outcomes.
const (
	JoinCreated        JoinOutcome = "CREATED"
	JoinExisting       JoinOutcome = "EXISTING"
	JoinReattach       JoinOutcome = "REATTACH" // the account's active entry returned to a new device
	JoinAccountInQueue JoinOutcome = "ERR_ACCOUNT_ALREADY_IN_QUEUE"
)

// JoinResult is the mapped outcome of join.lua.
type JoinResult struct {
	Outcome   JoinOutcome
	EntryID   string
	Seq       int64
	VisitorID string
	Status    string
	Gen       int64 // current token generation of VisitorID after the script's own writes
}

// HeartbeatTag is the heartbeat.lua outcome.
type HeartbeatTag string

// Heartbeat outcomes.
const (
	HeartbeatOK       HeartbeatTag = "OK"
	HeartbeatInvalid  HeartbeatTag = "INVALID"   // rotated-out or unknown token generation -> 401
	HeartbeatNotFound HeartbeatTag = "NOT_FOUND" // valid token, entry ended or never existed -> 404
)

// HeartbeatResult is the mapped heartbeat.lua reply.
type HeartbeatResult struct {
	Tag     HeartbeatTag
	EntryID string
	Status  string // WAITING (also after a revive) | ADMITTED | CLAIMED
	Seq     int64
	Revived bool
}

// LivenessResult is the mapped liveness.lua reply.
type LivenessResult struct {
	LeaseLost bool
	Staled    int
	Ended     int
}

// End outcome originated by the liveness sweeper.
const EndStaleTimeout = "STALE_TIMEOUT"

// EntryRetention is how long a terminal entry hash, and the fixed keys of a finished event, stay in Valkey before
// they expire (D-C8.2, P1-4, P3-13). Live entries (WAITING, ADMITTED, CLAIMED) never get a TTL.
const EntryRetention = 24 * time.Hour

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
	EndSweepMissed    = "SWEEP_MISSED"    // Queue sweeper only: deadline passed
	EndAlreadyActive  = "ALREADY_ACTIVE"  // promoter, WAITING entries only (Booking row is dead)
	EndPromoterOrphan = "PROMOTER_ORPHAN" // promoter: id in waiting without an entry hash (C5)
)

// EndResult is the typed reply of end_entry.lua.
type EndResult struct {
	Tag    string // OK | OK_CLEANUP | IGNORED
	Status string // final status for OK/OK_CLEANUP; current status (or NO_ENTRY) for IGNORED
}

// Changed reports whether the script mutated the entry.
func (r EndResult) Changed() bool { return r.Tag == "OK" || r.Tag == "OK_CLEANUP" }

// SoldOutBatch is the result of one EndWaitingSoldOut batch.
type SoldOutBatch struct {
	Scanned int // ids examined in this batch
	Ended   int // entries moved to SOLD_OUT
}

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
	SaleState string // NOT_STARTED | OPEN | CLOSED
	SoldOut   bool
	// SalesStartsMs/SalesEndsMs are the cached sale window (epoch ms), fed from Booking's supply read.
	// 0 means unknown: the join gate then reads Booking once and caches the answer.
	SalesStartsMs int64
	SalesEndsMs   int64
}

// AuditRecord is one entry_audit row.
type AuditRecord struct {
	EventID    string
	EntryID    string
	Seq        int64
	Action     string
	OccurredAt time.Time
	Metadata   map[string]any
	AccountID  string // "" for guest
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
	Units        int    `json:"units"`
	// No buyer fields (ADR-030): the buyer NIK/email are bound by Booking at hold.
	AccountID string `json:"accountId,omitempty"`
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

	// Event-level ledger and sale window (ADR-030, additive). Consumed from the golden fixture
	// docs/contracts/fixtures/internal-booking-supply.json, which Booking's handler test also matches.
	AllocatedUnits     int       `json:"allocatedUnits"`
	HeldUnits          int       `json:"heldUnits"`
	FreeUnits          int       `json:"freeUnits"`
	MaxTicketsPerOrder int       `json:"maxTicketsPerOrder"`
	SalesStartsAt      time.Time `json:"salesStartsAt"`
	SalesEndsAt        time.Time `json:"salesEndsAt"`
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
