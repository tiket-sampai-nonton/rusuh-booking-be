package usecase

import (
	"context"

	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/booking/internal/booking/domain/dto"
	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/booking/internal/booking/domain/entity"
	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/booking/internal/booking/port/inbound"
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

// GetSupply returns the event's free units as remaining tickets, and per-category sold and capacity for the Queue
// Service.
func (uc *GetEventSupply) GetSupply(ctx context.Context, eventID string) (dto.SupplySnapshot, error) {
	detail, err := uc.events.GetDetail(ctx, eventID)
	if err != nil {
		return dto.SupplySnapshot{}, err
	}
	if !detail.Event.Published {
		return dto.SupplySnapshot{}, inbound.ErrNotFound // draf tidak boleh dijual: Queue menolak join
	}

	cats := make([]dto.SupplyCategory, 0, len(detail.Categories))
	for _, c := range detail.Categories {
		cats = append(cats, dto.SupplyCategory{
			Code:  c.Category.Code,
			Name:  c.Category.Name,
			Sold:  c.Sold,
			Total: c.Capacity,
		})
	}

	// D-C5.2: the event's free units are what can still be sold; the stored SOLD_OUT status is not read.
	supply, err := uc.events.GetSupply(ctx, eventID)
	if err != nil {
		return dto.SupplySnapshot{}, err
	}
	remaining := supply.Free()
	soldOut := supply.SoldOut()
	if detail.Event.Status == entity.EventCancelled {
		remaining = 0 // dibatalkan: tidak ada tiket yang dijual
		soldOut = true
	}
	return dto.SupplySnapshot{
		EventID:            eventID,
		RemainingTickets:   remaining,
		SoldOut:            soldOut,
		Categories:         cats,
		AllocatedUnits:     supply.AllocatedUnits,
		HeldUnits:          supply.HeldUnits,
		FreeUnits:          supply.Free(),
		MaxTicketsPerOrder: detail.Event.MaxTicketsPerOrder,
		SalesStartsAt:      detail.Event.SalesStartsAt,
		SalesEndsAt:        detail.Event.SalesEndsAt,
	}, nil
}
