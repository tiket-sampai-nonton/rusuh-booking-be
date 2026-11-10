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
	if err := validateDescription(req.Description); err != nil {
		return err
	}
	if req.City != nil {
		city, err := normalizeCity(*req.City)
		if err != nil {
			return err
		}
		req.City = &city
	}
	if req.Genres != nil {
		genres, err := normalizeGenres(*req.Genres)
		if err != nil {
			return err
		}
		if genres == nil {
			genres = []string{} // keep a non-nil pointer to an empty set: "clear" must survive normalisation
		}
		req.Genres = &genres
	}
	return uc.repo.UpdateConfig(ctx, eventID, req)
}
