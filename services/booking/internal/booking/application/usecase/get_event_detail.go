package usecase

import (
	"context"

	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/booking/internal/booking/domain/dto"
	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/booking/internal/booking/domain/mapper"
	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/booking/internal/booking/port/inbound"
	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/booking/internal/booking/port/outbound"
)

// GetEventDetail serves one event with live availability from PostgreSQL.
type GetEventDetail struct {
	events outbound.IEventRepo
}

// NewGetEventDetail wires the detail usecase.
func NewGetEventDetail(events outbound.IEventRepo) *GetEventDetail {
	return &GetEventDetail{events: events}
}

// GetEventDetail validates the id, loads the event, and maps it to the detail DTO.
func (uc *GetEventDetail) GetEventDetail(ctx context.Context, eventID string) (dto.EventDetail, error) {
	if !isUUID(eventID) {
		return dto.EventDetail{}, inbound.Validation("eventId", "eventId harus UUID")
	}
	detail, err := uc.events.GetDetail(ctx, eventID)
	if err != nil {
		return dto.EventDetail{}, err
	}
	return mapper.ToEventDetail(detail), nil
}
