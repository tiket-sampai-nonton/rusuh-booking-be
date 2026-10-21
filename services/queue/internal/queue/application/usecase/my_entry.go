package usecase

import (
	"context"
	"encoding/json"

	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/queue/internal/queue/domain/dto"
	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/queue/internal/queue/domain/entity"
	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/queue/internal/queue/domain/mapper"
	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/queue/internal/queue/port/inbound"
	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/queue/internal/queue/port/outbound"
)

// GetMyEntry implements inbound.IMyEntryUsecase: returns the caller's own entry status (design §2.3).
// Position/zone are computed from the cached Status Document's serving context.
type GetMyEntry struct {
	store outbound.IQueueStore
}

// NewGetMyEntry accepts interfaces and returns the concrete usecase.
func NewGetMyEntry(store outbound.IQueueStore) *GetMyEntry {
	return &GetMyEntry{store: store}
}

// GetMyEntry finds the visitor's entry and maps it to the EntryStatus DTO (no PII).
func (u *GetMyEntry) GetMyEntry(ctx context.Context, in inbound.MyEntryInput) (dto.EntryStatus, error) {
	ent, ok, err := u.store.FindByVisitor(ctx, in.EventID, in.VisitorID)
	if err != nil {
		return dto.EntryStatus{}, err
	}
	if !ok {
		return dto.EntryStatus{}, entity.ErrNotFound
	}

	var servingSeq, removedAhead, guaranteedUntil, possibleUntil int64
	if raw, has, derr := u.store.GetStatusDoc(ctx, in.EventID); derr == nil && has {
		var doc dto.StatusDocument
		if json.Unmarshal(raw, &doc) == nil {
			servingSeq = doc.ServingSeq
			guaranteedUntil = doc.GuaranteedUntilSeq
			possibleUntil = doc.PossibleUntilSeq
			removedAhead = removedAheadFor(doc, ent.Seq.Int64())
		}
	}
	return mapper.ToEntryStatus(ent, servingSeq, removedAhead, guaranteedUntil, possibleUntil), nil
}

// removedAheadFor sums the removed-bucket counts for buckets entirely ahead of seq (design §2.2).
func removedAheadFor(doc dto.StatusDocument, seq int64) int64 {
	rb := doc.RemovedBuckets
	if rb == nil || rb.Size <= 0 {
		return 0
	}
	var total int64
	for i, c := range rb.Counts {
		bucketStart := rb.FromSeq + int64(i*rb.Size)
		if bucketStart < seq {
			total += c
		}
	}
	return total
}
