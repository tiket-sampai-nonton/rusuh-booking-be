package usecase

import (
	"context"

	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/booking/internal/booking/domain/dto"
	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/booking/internal/booking/domain/entity"
	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/booking/internal/booking/domain/mapper"
	vo "github.com/tiket-sampai-nonton/rusuh-booking-be/services/booking/internal/booking/domain/valueobject"
	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/booking/internal/booking/port/inbound"
	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/booking/internal/booking/port/outbound"
)

// GetTickets returns the e-tickets of a paid booking.
type GetTickets struct {
	bookings outbound.IBookingRepo
	tickets  outbound.ITicketRepo
}

// NewGetTickets wires the ticket usecase.
func NewGetTickets(bookings outbound.IBookingRepo, tickets outbound.ITicketRepo) *GetTickets {
	return &GetTickets{bookings: bookings, tickets: tickets}
}

// GetTickets returns the ticket list, or BOOKING_NOT_PAYABLE if the booking is not PAID.
func (uc *GetTickets) GetTickets(ctx context.Context, bookingID string) (dto.TicketList, error) {
	if !isUUID(bookingID) {
		return dto.TicketList{}, inbound.Validation("bookingId", "bookingId harus UUID")
	}
	booking, err := uc.bookings.GetByID(ctx, bookingID)
	if err != nil {
		return dto.TicketList{}, err
	}
	if booking.Status != vo.BookingPaid {
		return dto.TicketList{}, inbound.ErrNotPayable
	}
	tickets, err := uc.tickets.ListByBookingID(ctx, bookingID)
	if err != nil {
		return dto.TicketList{}, err
	}
	return mapper.ToTicketList(booking, tickets, qrPayload), nil
}

// qrPayload builds a QR string from the ticket's public code. The QR never embeds PII.
func qrPayload(t entity.Ticket) string { return "RBT:" + t.PublicCode }
