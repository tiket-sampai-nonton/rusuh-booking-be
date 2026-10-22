package usecase

import (
	"context"

	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/booking/internal/booking/domain/dto"
	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/booking/internal/booking/port/outbound"
)

// UpdateEventConfig updates mutable event configuration before rules are locked.
type UpdateEventConfig struct{ repo outbound.IEventWriteRepo }

// NewUpdateEventConfig wires the usecase.
func NewUpdateEventConfig(repo outbound.IEventWriteRepo) *UpdateEventConfig {
	return &UpdateEventConfig{repo: repo}
}

// Update applies configuration changes; returns ErrRulesLocked if rules are frozen.
func (uc *UpdateEventConfig) Update(ctx context.Context, eventID string, req dto.UpdateEventConfigRequest) error {
	return uc.repo.UpdateConfig(ctx, eventID, req)
}
