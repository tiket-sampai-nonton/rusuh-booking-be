package usecase

import (
	"context"

	"github.com/tiket-sampai-nonton/rusuh-booking-be/pkg/clock"
	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/booking/internal/booking/domain/dto"
	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/booking/internal/booking/domain/mapper"
	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/booking/internal/booking/port/inbound"
	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/booking/internal/booking/port/outbound"
)

// GetBooking fetches a booking by ID and asserts visitor ownership.
type GetBooking struct {
	repo  outbound.IBookingRepo
	clock clock.IClock
}

// NewGetBooking wires the usecase.
func NewGetBooking(repo outbound.IBookingRepo, clk clock.IClock) *GetBooking {
	return &GetBooking{repo: repo, clock: clk}
}

// GetBooking returns the booking DTO for the given ID. A visitor may only read its own booking.
func (uc *GetBooking) GetBooking(ctx context.Context, bookingID, visitorID string) (dto.Booking, error) {
	b, err := uc.repo.GetByID(ctx, bookingID)
	if err != nil {
		return dto.Booking{}, err
	}
	if b.VisitorID != visitorID {
		return dto.Booking{}, inbound.ErrVisitorMismatch
	}
	// editsRemaining is not tracked on the read model; the edit usecase is authoritative for it.
	return mapper.ToBooking(b, 0, uc.clock.Now()), nil
}
