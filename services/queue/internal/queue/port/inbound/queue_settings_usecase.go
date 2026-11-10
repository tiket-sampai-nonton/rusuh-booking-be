package inbound

import (
	"context"

	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/queue/internal/queue/domain/dto"
)

//go:generate go tool mockgen -source=queue_settings_usecase.go -destination=../../../../_mock/queue/port/inbound/queue_settings_usecase_mock.go -package=mock_inbound

// IGetQueueSettingsUsecase returns the stored queue settings of an event (admin, QSA-1).
type IGetQueueSettingsUsecase interface {
	Get(ctx context.Context, eventID string) (dto.QueueSettings, error)
}

// SetSettingsInput carries the PUT body plus the verified actor (token subject, never from the body).
type SetSettingsInput struct {
	EventID string
	Actor   string
	Patch   dto.QueueSettingsRequest
}

// ISetQueueSettingsUsecase creates or updates the queue settings of an event (admin, QSA-2/3/4).
// The bool is true when the row was created by this call (HTTP 201).
type ISetQueueSettingsUsecase interface {
	Set(ctx context.Context, in SetSettingsInput) (dto.QueueSettings, bool, error)
}
