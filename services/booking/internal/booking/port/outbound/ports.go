// Package outbound declares the ports the Booking usecases depend on: repositories, the event store,
// the outbox, idempotency, the unit of work, and external clients. Interfaces are prefixed I and live
// only here (structure.md). Usecases accept these; adapters implement them.
package outbound

import (
	"context"
	"time"

	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/booking/internal/booking/domain/dto"
	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/booking/internal/booking/domain/entity"
)

//go:generate go tool mockgen -source=ports.go -destination=../../../../_mock/booking/port/outbound/ports_mock.go -package=mock_outbound

// IEventRepo reads the event catalog and config (read-only, outside the stock transaction).
type IEventRepo interface {
	ListEvents(ctx context.Context, q dto.EventListQuery) ([]entity.Event, error)
	GetDetail(ctx context.Context, eventID string) (entity.EventDetail, error)
	GetByID(ctx context.Context, eventID string) (entity.Event, error)
}

// IAdmissionRepo manages admissions (allocations + sessions).
type IAdmissionRepo interface {
	FindByJTI(ctx context.Context, jti string) (entity.Admission, error)
	FindActiveByVisitor(ctx context.Context, eventID, visitorID string) (entity.Admission, error)
	FindActiveByEvent(ctx context.Context, eventID, visitorID string) (entity.Admission, error)
	GetByID(ctx context.Context, id string) (entity.Admission, error)
	// Activate flips a RESERVED admission to ACTIVE (claim). Returns ErrNotFound when the jti is unknown.
	Activate(ctx context.Context, jti string, at time.Time) (entity.Admission, error)
	// ReserveBatch reserves one admission per entry in a single transaction (config snapshot copied
	// from the event); it returns one outcome per entry. Idempotent per queue_entry_id.
	ReserveBatch(ctx context.Context, eventID string, entries []ReserveEntry, at time.Time) ([]ReserveOutcome, error)
	// Release compensates a saga: RELEASED + allocated_units -= units.
	Release(ctx context.Context, allocationID string) (bool, error)
	// DueSessions scans admissions whose expiry has lapsed (session sweeper).
	DueSessions(ctx context.Context, now time.Time, limit int) ([]entity.Admission, error)
	// ExpireSession releases one admission's allocation/held stock in its own transaction.
	ExpireSession(ctx context.Context, admissionID string, now time.Time) error
}

// ReserveEntry is one admission reservation request (already decoded, nik hash as bytes).
type ReserveEntry struct {
	QueueEntryID   string
	Seq            int64
	VisitorID      string
	BuyerNIKHash   []byte
	BuyerNIKMasked string
	BuyerEmail     string
	AccountID      string
	Units          int
	AdmissionJTI   string
}

// ReserveOutcome is the result of reserving one entry.
type ReserveOutcome struct {
	QueueEntryID  string
	Outcome       string // GRANTED | NO_SUPPLY_NOW | SOLD_OUT | BUYER_QUOTA_EXHAUSTED | ALREADY_ACTIVE
	AllocationID  string
	Units         int
	ClaimDeadline *time.Time
}

// IBookingRepo reads booking read models (writes go through IUnitOfWork).
type IBookingRepo interface {
	GetByID(ctx context.Context, id string) (entity.Booking, error)
	ListByAccount(ctx context.Context, accountID, cursor string, limit int) ([]dto.MyBookingItem, string, error)
}

// IBookingDueScanner finds bookings whose payment window has lapsed (expiry sweeper).
type IBookingDueScanner interface {
	DueBookingIDs(ctx context.Context, now time.Time, limit int) ([]string, error)
}

// ITicketRepo reads issued tickets (writes go through IUnitOfWork during settlement).
type ITicketRepo interface {
	ListByBookingID(ctx context.Context, bookingID string) ([]entity.Ticket, error)
}

// IQuotaRepo reads buyer quota (writes go through IUnitOfWork).
type IQuotaRepo interface {
	CountHeldAndPaid(ctx context.Context, eventID string, buyerNIKHash []byte) (held, paid, max int, err error)
}

// IAttendeeChecker probes whether an attendee NIK is already registered (HELD/PAID) for an event.
type IAttendeeChecker interface {
	IsRegistered(ctx context.Context, eventID string, nikHash []byte) (bool, error)
	// RecordCheck inserts a dedupe row; counted=false means the NIK was already checked this session.
	RecordCheck(ctx context.Context, admissionID string, nikHash []byte) (counted bool, err error)
}

// IPaymentClient resolves whether a booking was in fact paid at the gateway (expiry sweeper).
type IPaymentClient interface {
	Resolve(ctx context.Context, bookingID string) (ResolveResult, error)
}

// ResolveResult is the Payment Service's answer to a resolve probe.
type ResolveResult struct {
	Paid bool
}

// AdmissionClaims is the verified content of an admission token issued by the Queue Service.
type AdmissionClaims struct {
	JTI       string
	EventID   string
	VisitorID string
}

// IAdmissionTokenVerifier verifies the one-time EdDSA admission token minted by the Queue Service.
type IAdmissionTokenVerifier interface {
	Verify(token string) (AdmissionClaims, error)
}
