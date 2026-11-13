package outbound

import (
	"context"
	"time"
)

// HoldLine is one requested ticket line, already validated and with PII reduced to hash/mask/enc.
type HoldLine struct {
	// ItemID is the booking_items.id of this line. The usecase generates it (ids.NewID), so the hold inserts all
	// items in one statement without RETURNING.
	ItemID            string
	CategoryID        string
	UnitPriceMinor    int64
	AttendeeNIKHash   []byte
	AttendeeNIKEnc    []byte
	AttendeeNIKMasked string
	AttendeeName      string
}

// HoldSpec is everything IUnitOfWork.Hold needs to perform the atomic hold in one transaction.
type HoldSpec struct {
	BookingID        string
	PublicRef        string
	EventID          string
	AdmissionID      string
	VisitorID        string
	BuyerNIKHash     []byte
	BuyerNIKMasked   string
	BuyerEmailEnc    string
	AccountID        string
	AmountMinor      int64
	PaymentExpiresAt time.Time
	Lines            []HoldLine
	CorrelationID    string
	// For admission lock (6.R2) and EDITABLE transition (6.R3):
	CfgEditEnabled bool // from admission.CfgEditEnabled — if false, lock immediately after hold

	// Buyer-NIK refusal policy (R10.6) and the app clock used for the throttle window.
	Now           time.Time
	RefusalBudget int           // refusals allowed before the throttle starts
	ThrottleBase  time.Duration // first throttle step; doubles per extra refusal
	ThrottleMax   time.Duration
}

// HoldResult is the committed hold outcome.
type HoldResult struct {
	BookingID        string
	PublicRef        string
	Version          int
	Status           string
	AmountMinor      int64
	PaymentExpiresAt time.Time
	// Refused is set when the hold was refused for a reason whose attempt counter must survive (the
	// transaction committed): currently only "BUYER_NIK_LIMIT_REACHED".
	Refused string
	// ItemIDs holds the booking_items.id per line, in spec.Lines order (the HoldLine.ItemID values); len == len(spec.Lines).
	ItemIDs []string
}

// RefusedBuyerNikLimit is HoldResult.Refused when the buyer NIK is at its ticket cap.
const RefusedBuyerNikLimit = "BUYER_NIK_LIMIT_REACHED"

// ReplaceSpec is everything IUnitOfWork.ReplaceItems needs for an atomic edit (no TTL reset).
type ReplaceSpec struct {
	BookingID       string
	AdmissionID     string
	EventID         string
	BuyerNIKHash    []byte
	ExpectedVersion int
	NewAmountMinor  int64
	Lines           []HoldLine
	CorrelationID   string
	Now             time.Time // app clock; zero falls back to the wall clock
}

// SettleSpec is everything IUnitOfWork.Settle needs for an idempotent settlement.
type SettleSpec struct {
	BookingID         string
	Method            string
	SettlementTime    time.Time                     // gateway settlement time from payment.succeeded (R9.1, R9.3)
	AmountMinor       int64                         // amount the gateway charged; 0 = unknown, skip the check (P2-8)
	TicketCodeFn      func(itemID string) string    // supplies a public ticket code per item
	TicketLinkTokenFn func(bookingID string) string // supplies the ticket-link token (may be nil → empty)
	CorrelationID     string
}

// ExpireSpec is everything IUnitOfWork.Expire needs to release a booking's held stock.
type ExpireSpec struct {
	BookingID     string
	Reason        string // PAYMENT_TIMEOUT | SESSION_EXPIRED | CANCELLED
	Review        bool   // true → mark REVIEW (payment found) instead of EXPIRED
	CorrelationID string
}

// DeadlineSpec is the input for IUnitOfWork.ExtendDeadline.
type DeadlineSpec struct {
	BookingID     string
	Reason        string
	Seconds       int
	CorrelationID string
}

// PaymentLockSpec is the input for IUnitOfWork.PaymentLock.
type PaymentLockSpec struct {
	BookingID     string
	AttemptKey    string
	CorrelationID string
	Now           time.Time // app clock; zero falls back to the wall clock
}

// PaymentLockResult is the outcome of the payment-lock operation.
type PaymentLockResult struct {
	Outcome          string // OK | BOOKING_NOT_PAYABLE | PAYMENT_EXPIRED
	Version          int
	AmountMinor      int64
	PaymentExpiresAt time.Time
}

// IUnitOfWork performs the stock/money commands in one PostgreSQL transaction each, hiding the fixed
// lock ordering (supply → category → quota → booking), the event-store append, the read-model
// projection, and the outbox write. Each method retries internally on event-store concurrency.
type IUnitOfWork interface {
	Hold(ctx context.Context, spec HoldSpec) (HoldResult, error)
	ReplaceItems(ctx context.Context, spec ReplaceSpec) (int, error)
	Settle(ctx context.Context, spec SettleSpec) (bool, error) // settled=true only when the booking became PAID (REVIEW and already-PAID are false)
	Expire(ctx context.Context, spec ExpireSpec) (bool, error) // acted=false when the booking was already terminal
	ExtendDeadline(ctx context.Context, spec DeadlineSpec) error
	PaymentLock(ctx context.Context, spec PaymentLockSpec) (PaymentLockResult, error)
	// StartVerifying is Phase A of the expiry sweeper: transitions PENDING_PAYMENT past its deadline
	// to VERIFYING so resolve can be called outside the transaction (design §7.2). Idempotent: returns
	// acted=false when the booking is already VERIFYING or terminal, or not yet past its deadline.
	StartVerifying(ctx context.Context, bookingID string, verifyGraceSeconds int) (acted bool, err error)
}

// IdempotencyRecord is a stored idempotent request.
type IdempotencyRecord struct {
	State          string // IN_PROGRESS | COMPLETED
	RequestHash    []byte
	ResponseStatus int
	ResponseBody   []byte
}

// IIdempotencyStore deduplicates mutating requests by (scope, owner, key).
type IIdempotencyStore interface {
	// Begin inserts an IN_PROGRESS record, or returns the existing one (found=true) for replay/conflict.
	Begin(ctx context.Context, scope, ownerID, key string, requestHash []byte, ttl time.Duration) (rec IdempotencyRecord, found bool, err error)
	Complete(ctx context.Context, scope, ownerID, key string, status int, body []byte) error
	// Abort deletes an IN_PROGRESS record with the same request hash so a refused request can be retried
	// under the same key. It never deletes a COMPLETED record.
	Abort(ctx context.Context, scope, ownerID, key string, requestHash []byte) error
}

// IBuyerPII hashes, seals and fingerprints buyer data at hold (ADR-030). Plaintext never reaches storage,
// logs or events: the NIK is kept as HMAC + mask, the email as AES-256-GCM base64 with the Notification AAD.
type IBuyerPII interface {
	HashNIK(nik string) []byte
	SealNIK(nik string) ([]byte, error)
	SealBuyerEmail(email string) (string, error)
	// Fingerprint is a keyed HMAC of arbitrary bytes, used for the idempotency request hash.
	Fingerprint(data []byte) []byte
}
