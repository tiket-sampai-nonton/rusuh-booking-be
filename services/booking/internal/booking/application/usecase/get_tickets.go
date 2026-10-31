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

// TicketLinkVerifier checks the token carried by the e-ticket email link.
type TicketLinkVerifier interface {
	Valid(bookingID, token string) bool
}

// GetTickets returns the e-tickets of a paid booking to its owner (or the holder of the email link).
type GetTickets struct {
	bookings outbound.IBookingRepo
	tickets  outbound.ITicketRepo
	links    TicketLinkVerifier
}

// NewGetTickets wires the ticket usecase.
func NewGetTickets(bookings outbound.IBookingRepo, tickets outbound.ITicketRepo, links TicketLinkVerifier) *GetTickets {
	return &GetTickets{bookings: bookings, tickets: tickets, links: links}
}

// GetTickets returns the ticket list. Access is the booking's visitor (verified token), its buyer account (verified
// bearer), or the ticket-link token from the e-ticket email; anyone else gets NOT_FOUND, as if the booking did not exist,
// so a booking id alone never opens the tickets and the QR codes in them.
func (uc *GetTickets) GetTickets(ctx context.Context, bookingID string, access inbound.TicketAccess) (dto.TicketList, error) {
	if !isUUID(bookingID) {
		return dto.TicketList{}, inbound.Validation("bookingId", "bookingId harus UUID")
	}
	booking, err := uc.bookings.GetByID(ctx, bookingID)
	if err != nil {
		return dto.TicketList{}, err
	}
	owner := access.VisitorID != "" && access.VisitorID == booking.VisitorID
	account := access.AccountID != "" && access.AccountID == booking.AccountID
	link := access.LinkToken != "" && uc.links != nil && uc.links.Valid(bookingID, access.LinkToken)
	if !owner && !account && !link {
		return dto.TicketList{}, inbound.ErrNotFound
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
