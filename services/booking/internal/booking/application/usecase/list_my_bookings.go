package usecase

import (
	"context"

	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/booking/internal/booking/domain/dto"
	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/booking/internal/booking/port/inbound"
	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/booking/internal/booking/port/outbound"
)

const (
	defaultHistoryLimit = 20
	maxHistoryLimit     = 50
)

// ListMyBookings lists a logged-in buyer's bookings with strict ownership and cursor pagination.
type ListMyBookings struct {
	bookings outbound.IBookingRepo
}

// NewListMyBookings wires the history usecase.
func NewListMyBookings(bookings outbound.IBookingRepo) *ListMyBookings {
	return &ListMyBookings{bookings: bookings}
}

// ListMyBookings returns the buyer's bookings. accountID comes from the verified bearer token.
func (uc *ListMyBookings) ListMyBookings(ctx context.Context, accountID, cursor string, limit int) (dto.MyBookings, error) {
	if accountID == "" {
		return dto.MyBookings{}, inbound.ErrUnauthorized
	}
	if limit <= 0 {
		limit = defaultHistoryLimit
	}
	if limit > maxHistoryLimit {
		limit = maxHistoryLimit
	}
	items, next, err := uc.bookings.ListByAccount(ctx, accountID, cursor, limit)
	if err != nil {
		return dto.MyBookings{}, err
	}
	var nextCursor *string
	if next != "" {
		nextCursor = &next
	}
	if items == nil {
		items = []dto.MyBookingItem{}
	}
	return dto.MyBookings{Bookings: items, NextCursor: nextCursor}, nil
}
