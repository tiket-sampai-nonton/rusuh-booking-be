package usecase

import (
	"context"

	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/booking/internal/booking/domain/dto"
	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/booking/internal/booking/domain/entity"
	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/booking/internal/booking/port/outbound"
)

// GetEventSupply computes the stock snapshot from the event detail for the Queue status publisher.
type GetEventSupply struct {
	events outbound.IEventRepo
}

// NewGetEventSupply wires the usecase.
func NewGetEventSupply(events outbound.IEventRepo) *GetEventSupply {
	return &GetEventSupply{events: events}
}

// GetSupply returns remaining tickets and per-category availability for the Queue Service.
func (uc *GetEventSupply) GetSupply(ctx context.Context, eventID string) (dto.SupplySnapshot, error) {
	detail, err := uc.events.GetDetail(ctx, eventID)
	if err != nil {
		return dto.SupplySnapshot{}, err
	}

	remaining := 0
	cats := make([]dto.SupplyCategory, 0, len(detail.Categories))
	for _, c := range detail.Categories {
		remaining += c.Available
		cats = append(cats, dto.SupplyCategory{
			Code:  c.Category.Code,
			Name:  c.Category.Name,
			Sold:  0,
			Total: c.Available,
		})
	}

	soldOut := detail.Event.Status == entity.EventSoldOut || remaining == 0

	return dto.SupplySnapshot{
		EventID:          eventID,
		RemainingTickets: remaining,
		SoldOut:          soldOut,
		Categories:       cats,
	}, nil
}
