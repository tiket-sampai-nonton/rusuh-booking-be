package usecase

import (
	"context"
	"errors"

	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/booking/internal/booking/domain/dto"
	vo "github.com/tiket-sampai-nonton/rusuh-booking-be/services/booking/internal/booking/domain/valueobject"
	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/booking/internal/booking/port/inbound"
	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/booking/internal/booking/port/outbound"
)

// ReleaseAllocation releases an orphaned allocation as saga compensation (ADR-018). Called by
// Queue when an admitted entry is abandoned without claiming (e.g. claim sweeper marks MISSED).
type ReleaseAllocation struct{ admissions outbound.IAdmissionRepo }

// NewReleaseAllocation wires the usecase.
func NewReleaseAllocation(admissions outbound.IAdmissionRepo) *ReleaseAllocation {
	return &ReleaseAllocation{admissions: admissions}
}

// Release validates the allocationID and delegates to the repository.
// Returns released=false when the admission is already terminal (idempotent).
func (uc *ReleaseAllocation) Release(ctx context.Context, allocationID string) (dto.ReleaseResponse, error) {
	if !isUUID(allocationID) {
		return dto.ReleaseResponse{}, inbound.Validation("allocationId", "allocationId harus UUID")
	}
	released, err := uc.admissions.Release(ctx, allocationID, vo.EndUnclaimed)
	if errors.Is(err, inbound.ErrBookingLocked) {
		// A hold exists: the admission is not releasable. Best-effort internal caller just learns "not released".
		return dto.ReleaseResponse{Released: false}, nil
	}
	if err != nil {
		return dto.ReleaseResponse{}, err
	}
	return dto.ReleaseResponse{Released: released}, nil
}
