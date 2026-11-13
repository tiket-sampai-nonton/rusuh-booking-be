package usecase

import (
	"context"
	"encoding/json"

	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/queue/internal/queue/domain/entity"
	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/queue/internal/queue/port/outbound"
)

// GetStatus implements inbound.IStatusUsecase: returns the public Status Document (design §2.2) as the stored JSON.
// The document is produced by the Status Publisher worker; this usecase only reads it, through the process cache
// (D-C8.9), and passes the bytes on unchanged.
type GetStatus struct {
	docs outbound.IStatusDocReader
}

// NewGetStatus accepts interfaces and returns the concrete usecase.
func NewGetStatus(docs outbound.IStatusDocReader) *GetStatus {
	return &GetStatus{docs: docs}
}

// GetStatus returns the event's document. A missing document means the event is unknown → ErrNotFound.
func (u *GetStatus) GetStatus(ctx context.Context, eventID string) (json.RawMessage, error) {
	raw, _, ok, err := u.docs.Get(ctx, eventID)
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, entity.ErrNotFound
	}
	return raw, nil
}
