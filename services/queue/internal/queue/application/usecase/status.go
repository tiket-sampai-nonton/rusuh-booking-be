package usecase

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/queue/internal/queue/domain/dto"
	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/queue/internal/queue/domain/entity"
	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/queue/internal/queue/port/outbound"
)

// GetStatus implements inbound.IStatusUsecase: returns the cached public Status Document (design §2.2).
// The document is produced by the Status Publisher worker; this usecase only reads it.
type GetStatus struct {
	store outbound.IQueueStore
}

// NewGetStatus accepts interfaces and returns the concrete usecase.
func NewGetStatus(store outbound.IQueueStore) *GetStatus {
	return &GetStatus{store: store}
}

// GetStatus reads q:{e}:status. A missing document means the event is unknown → ErrNotFound.
func (u *GetStatus) GetStatus(ctx context.Context, eventID string) (dto.StatusDocument, error) {
	raw, ok, err := u.store.GetStatusDoc(ctx, eventID)
	if err != nil {
		return dto.StatusDocument{}, err
	}
	if !ok {
		return dto.StatusDocument{}, entity.ErrNotFound
	}
	var doc dto.StatusDocument
	if err := json.Unmarshal(raw, &doc); err != nil {
		return dto.StatusDocument{}, fmt.Errorf("status: unmarshal document: %w", err)
	}
	return doc, nil
}
