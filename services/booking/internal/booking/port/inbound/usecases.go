// Package inbound declares the Booking usecase ports (I<Usecase>) that HTTP handlers, consumers, and
// workers depend on. Implementations live in application/usecase.
package inbound

import (
	"context"

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
	GetTickets(ctx context.Context, bookingID string) (dto.TicketList, error)
}

// IBookingHistoryUsecase lists a logged-in buyer's bookings.
type IBookingHistoryUsecase interface {
	ListMyBookings(ctx context.Context, accountID, cursor string, limit int) (dto.MyBookings, error)
}

// SettleCommand carries a payment.succeeded event.
type SettleCommand struct {
	BookingID     string
	AttemptID     string
	AmountMinor   int64
	Method        string
	CorrelationID string
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
