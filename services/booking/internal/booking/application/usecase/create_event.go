package usecase

import (
	"context"

	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/booking/internal/booking/domain/dto"
	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/booking/internal/booking/port/inbound"
	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/booking/internal/booking/port/outbound"
)

// CreateEvent creates a new event with categories and supply (admin seed).
type CreateEvent struct {
	repo outbound.IEventWriteRepo
	ids  IDGen
}

// NewCreateEvent wires the usecase.
func NewCreateEvent(repo outbound.IEventWriteRepo, ids IDGen) *CreateEvent {
	return &CreateEvent{repo: repo, ids: ids}
}

// Create validates, generates an ID, and persists the new event.
func (uc *CreateEvent) Create(ctx context.Context, req dto.CreateEventRequest) (string, error) {
	if len(req.Categories) == 0 {
		return "", inbound.Validation("categories", "minimal satu kategori harus ada")
	}
	id := uc.ids.NewID()
	if err := uc.repo.CreateEvent(ctx, id, req); err != nil {
		return "", err
	}
	return id, nil
}
