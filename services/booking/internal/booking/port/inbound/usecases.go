// Package inbound declares the Booking usecase ports (I<Usecase>) that HTTP handlers, consumers, and
// workers depend on. Implementations live in application/usecase.
package inbound

import (
	"context"
	"io"
	"time"

	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/booking/internal/booking/domain/dto"
)

//go:generate go tool mockgen -source=usecases.go -destination=../../../../_mock/booking/port/inbound/usecases_mock.go -package=mock_inbound

// IEventCatalogUsecase lists events for the public catalog.
type IEventCatalogUsecase interface {
	ListEvents(ctx context.Context, q dto.EventListQuery) (dto.EventList, error)
}

// IEventDetailUsecase returns one event with live availability.
type IEventDetailUsecase interface {
	GetEventDetail(ctx context.Context, eventID string) (dto.EventDetail, error)
}

// ICreateEventUsecase creates a new event with categories and supply.
type ICreateEventUsecase interface {
	Create(ctx context.Context, req dto.CreateEventRequest) (string, error)
}

// IUpdateEventConfigUsecase updates mutable event configuration.
type IUpdateEventConfigUsecase interface {
	Update(ctx context.Context, eventID string, req dto.UpdateEventConfigRequest) error
}

// IAllocationUsecase reserves admissions for a batch of queue entries (internal, called by Queue).
type IAllocationUsecase interface {
	AllocateBatch(ctx context.Context, req dto.AllocationBatchRequest) (dto.AllocationBatchResponse, error)
}

// ISessionUsecase opens and reads booking sessions.
type ISessionUsecase interface {
	CreateSession(ctx context.Context, req dto.CreateSessionRequest, visitorID string) (dto.BookingSession, error)
	GetSession(ctx context.Context, sessionID, visitorID string) (dto.BookingSession, error)
	GetCurrentSession(ctx context.Context, eventID, visitorID string) (dto.BookingSession, error)
}

// IAttendeeCheckUsecase probes an attendee NIK during selection.
type IAttendeeCheckUsecase interface {
	CheckAttendee(ctx context.Context, sessionID string, req dto.AttendeeCheckRequest) (dto.AttendeeCheck, error)
}

// IStreamSessionUsecase pushes live availability + timer events over SSE.
type IStreamSessionUsecase interface {
	Stream(ctx context.Context, sessionID, visitorID string, w io.Writer, flush func()) error
}

// HoldCommand carries the hold request plus the ambient request identity.
type HoldCommand struct {
	Request        dto.HoldRequest
	VisitorID      string
	IdempotencyKey string
	CorrelationID  string
}

// IHoldUsecase performs the atomic hold (invariant #1 — no oversell).
type IHoldUsecase interface {
	Hold(ctx context.Context, cmd HoldCommand) (dto.Booking, error)
}

// EditCommand carries the replace request plus identity.
type EditCommand struct {
	BookingID     string
	Request       dto.ReplaceItemsRequest
	VisitorID     string
	CorrelationID string
}

// IEditUsecase replaces a booking's items before payment (no TTL reset).
type IEditUsecase interface {
	ReplaceItems(ctx context.Context, cmd EditCommand) (dto.Booking, error)
}

// ITicketUsecase returns e-tickets for a paid booking.
type ITicketUsecase interface {
	GetTickets(ctx context.Context, bookingID string, access TicketAccess) (dto.TicketList, error)
}

// TicketAccess is who is asking for the tickets, already verified by the HTTP layer: the visitor token's subject, the
// buyer account of a bearer token, or the unverified ticket-link token (the usecase checks it against the booking).
type TicketAccess struct {
	VisitorID string
	AccountID string
	LinkToken string
}

// IGetBookingUsecase returns a booking by ID (caller must own the visitor token).
type IGetBookingUsecase interface {
	GetBooking(ctx context.Context, bookingID, visitorID string) (dto.Booking, error)
}

// ICancelBookingUsecase cancels an unpaid booking (SHOULD priority). Settles if payment raced.
type ICancelBookingUsecase interface {
	Cancel(ctx context.Context, bookingID, visitorID string) (dto.Booking, error)
}

// IBookingHistoryUsecase lists a logged-in buyer's bookings.
type IBookingHistoryUsecase interface {
	ListMyBookings(ctx context.Context, accountID, cursor string, limit int) (dto.MyBookings, error)
}

// SettleCommand carries a payment.succeeded event.
type SettleCommand struct {
	BookingID      string
	AttemptID      string
	AmountMinor    int64
	SettlementTime time.Time // from payment.succeeded.v1 payload; used for timeliness check (R9.3)
	Method         string
	CorrelationID  string
}

// ISettlementUsecase settles a paid booking (consumed from payment.succeeded).
type ISettlementUsecase interface {
	Settle(ctx context.Context, cmd SettleCommand) error
}

// IExpiryUsecase expires or reviews a booking whose payment window lapsed (sweeper).
type IExpiryUsecase interface {
	ExpireBooking(ctx context.Context, bookingID string) error
}

// ISessionSweeper releases expired admissions (sweeper).
type ISessionSweeper interface {
	SweepSessions(ctx context.Context) error
}

// IExpirySweeper scans for expired bookings and resolves them (sweeper).
type IExpirySweeper interface {
	SweepExpiries(ctx context.Context) error
}

// ISupplyUsecase returns the stock snapshot for an event (internal, called by Queue Service).
type ISupplyUsecase interface {
	GetSupply(ctx context.Context, eventID string) (dto.SupplySnapshot, error)
}

// IGetPayableUsecase returns the payment billing view of a booking (called by Payment Service).
type IGetPayableUsecase interface {
	GetPayable(ctx context.Context, bookingID string) (dto.Payable, error)
}

// IPaymentLockUsecase locks a booking before a payment attempt (called by Payment Service).
type IPaymentLockUsecase interface {
	Lock(ctx context.Context, bookingID, attemptKey string) (dto.PaymentLockResponse, error)
}

// IDeadlineExtensionUsecase extends the payment deadline once when the gateway is degraded.
type IDeadlineExtensionUsecase interface {
	Extend(ctx context.Context, bookingID, reason string, seconds int) error
}

// ICancelSessionUsecase releases an admission allocation when buyer leaves without buying.
type ICancelSessionUsecase interface {
	CancelSession(ctx context.Context, sessionID, visitorID string) error
}

// IReleaseAllocationUsecase releases an orphaned allocation (saga compensation, ADR-018).
// Called by Queue when an admitted entry is abandoned without claiming.
type IReleaseAllocationUsecase interface {
	Release(ctx context.Context, allocationID string) (dto.ReleaseResponse, error)
}
