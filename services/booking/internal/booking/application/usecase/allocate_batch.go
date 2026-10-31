package usecase

import (
	"context"
	"encoding/hex"

	"github.com/tiket-sampai-nonton/rusuh-booking-be/pkg/clock"

	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/booking/internal/booking/domain/dto"
	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/booking/internal/booking/port/inbound"
	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/booking/internal/booking/port/outbound"
)

// AllocateBatch reserves admissions for a batch of queue entries (invariant #2 — Jangan PHP). The
// reservation is atomic per batch and idempotent per queue_entry_id (handled by the repository).
type AllocateBatch struct {
	admissions outbound.IAdmissionRepo
	ids        IDGen
	clock      clock.IClock
}

// NewAllocateBatch wires the allocation usecase.
func NewAllocateBatch(admissions outbound.IAdmissionRepo, ids IDGen, clk clock.IClock) *AllocateBatch {
	return &AllocateBatch{admissions: admissions, ids: ids, clock: clk}
}

// AllocateBatch validates the request and reserves each entry.
func (uc *AllocateBatch) AllocateBatch(ctx context.Context, req dto.AllocationBatchRequest) (dto.AllocationBatchResponse, error) {
	if !isUUID(req.EventID) {
		return dto.AllocationBatchResponse{}, inbound.Validation("eventId", "eventId harus UUID")
	}
	if len(req.Entries) == 0 {
		return dto.AllocationBatchResponse{}, inbound.Validation("entries", "minimal satu entry")
	}

	entries := make([]outbound.ReserveEntry, 0, len(req.Entries))
	for _, e := range req.Entries {
		nikHash, err := hex.DecodeString(e.BuyerNIKHash)
		if err != nil || len(nikHash) == 0 {
			return dto.AllocationBatchResponse{}, inbound.Validation("buyerNikHash", "buyerNikHash harus hex HMAC")
		}
		units := e.Units
		if units < 1 || units > 3 {
			units = 1
		}
		entries = append(entries, outbound.ReserveEntry{
			QueueEntryID:   e.QueueEntryID,
			Seq:            e.Seq,
			VisitorID:      e.VisitorID,
			BuyerNIKHash:   nikHash,
			BuyerNIKMasked: e.BuyerNIKMasked,
			BuyerEmailEnc:  e.BuyerEmailEnc,
			AccountID:      e.AccountID,
			Units:          units,
			AdmissionJTI:   uc.ids.NewID(),
		})
	}

	outcomes, err := uc.admissions.ReserveBatch(ctx, req.EventID, entries, uc.clock.Now())
	if err != nil {
		return dto.AllocationBatchResponse{}, err
	}

	results := make([]dto.AllocationResult, 0, len(outcomes))
	for _, o := range outcomes {
		results = append(results, dto.AllocationResult{
			QueueEntryID:  o.QueueEntryID,
			Outcome:       o.Outcome,
			AllocationID:  o.AllocationID,
			Units:         o.Units,
			ClaimDeadline: o.ClaimDeadline,
		})
	}
	return dto.AllocationBatchResponse{Results: results}, nil
}
