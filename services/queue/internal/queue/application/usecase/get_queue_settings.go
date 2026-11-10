package usecase

import (
	"context"

	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/queue/internal/queue/domain/dto"
	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/queue/internal/queue/domain/entity"
	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/queue/internal/queue/domain/mapper"
	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/queue/internal/queue/port/outbound"
)

// GetQueueSettings implements inbound.IGetQueueSettingsUsecase (QSA-1). Read only: no Booking call, no write.
type GetQueueSettings struct{ settings outbound.IEventSettingsRepo }

// NewGetQueueSettings accepts interfaces and returns the concrete usecase.
func NewGetQueueSettings(settings outbound.IEventSettingsRepo) *GetQueueSettings {
	return &GetQueueSettings{settings: settings}
}

// Get returns the stored settings or entity.ErrSettingsNotFound when the event has no row (no defaults are invented).
func (u *GetQueueSettings) Get(ctx context.Context, eventID string) (dto.QueueSettings, error) {
	s, found, err := u.settings.Get(ctx, eventID)
	if err != nil {
		return dto.QueueSettings{}, err
	}
	if !found {
		return dto.QueueSettings{}, entity.ErrSettingsNotFound
	}
	return mapper.ToQueueSettings(s), nil
}
