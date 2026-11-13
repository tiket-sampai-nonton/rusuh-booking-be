package usecase

import (
	"context"
	"encoding/hex"

	"github.com/tiket-sampai-nonton/rusuh-booking-be/pkg/clock"

	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/booking/internal/booking/domain/dto"
	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/booking/internal/booking/port/inbound"
	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/booking/internal/booking/port/outbound"
)

// allocateBatch reserves admissions for a batch of queue entries (invariant #2 — Jangan PHP). The
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

// maxBatchEntries caps one allocation batch (D-C7.2): the repository locks the event's supply once per batch, and Queue's
// promoter sends at most this many entries per call (QUEUE_PROMOTE_BATCH_MAX, validated to the same 100).
const maxBatchEntries = 100

// AllocateBatch validates the request and reserves each entry.
func (uc *AllocateBatch) AllocateBatch(ctx context.Context, req dto.AllocationBatchRequest) (dto.AllocationBatchResponse, error) {
	if !isUUID(req.EventID) {
		return dto.AllocationBatchResponse{}, inbound.Validation("eventId", errEventIDNotUUID)
	}
	if len(req.Entries) == 0 {
		return dto.AllocationBatchResponse{}, inbound.Validation("entries", "minimal satu entry")
	}
	if len(req.Entries) > maxBatchEntries {
		return dto.AllocationBatchResponse{}, inbound.Validation("entries", "maksimal 100 entri")
	}

	entries := make([]outbound.ReserveEntry, 0, len(req.Entries))
	for _, e := range req.Entries {
		// The buyer NIK is optional: in the anonymous flow (ADR-030) it is bound at hold, not at reservation.
		var nikHash []byte
		if e.BuyerNIKHash != "" {
			var err error
			nikHash, err = hex.DecodeString(e.BuyerNIKHash)
			if err != nil || len(nikHash) == 0 {
				return dto.AllocationBatchResponse{}, inbound.Validation("buyerNikHash", "buyerNikHash harus hex HMAC")
			}
		}
		// Upper clamp to the event's max_tickets_per_order happens in the repository (reads the event row).
		units := e.Units
		if units < 1 {
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
