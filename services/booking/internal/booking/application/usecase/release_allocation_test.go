package usecase_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/booking/internal/booking/application/usecase"
	vo "github.com/tiket-sampai-nonton/rusuh-booking-be/services/booking/internal/booking/domain/valueobject"
	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/booking/internal/booking/port/inbound"
)

const allocationID = "11111111-1111-1111-1111-111111111111"

func TestReleaseAllocation_ReleasesWhenFound(t *testing.T) {
	repo := &fakeAdmissionRepo{}
	uc := usecase.NewReleaseAllocation(repo)
	resp, err := uc.Release(context.Background(), allocationID)
	require.NoError(t, err)
	assert.True(t, resp.Released)
	assert.Contains(t, repo.released, allocationID)
}

func TestReleaseAllocation_AlreadyTerminal_ReturnsFalse(t *testing.T) {
	repo := &fakeAdmissionRepo{releaseResult: boolPtr(false)}
	uc := usecase.NewReleaseAllocation(repo)
	resp, err := uc.Release(context.Background(), allocationID)
	require.NoError(t, err)
	assert.False(t, resp.Released)
}

func TestReleaseAllocation_InvalidUUID(t *testing.T) {
	uc := usecase.NewReleaseAllocation(&fakeAdmissionRepo{})
	_, err := uc.Release(context.Background(), "not-a-uuid")
	assert.Error(t, err)
}

func TestReleaseAllocation_NotFound(t *testing.T) {
	uc := usecase.NewReleaseAllocation(&fakeAdmissionRepo{getErr: inbound.ErrNotFound})
	_, err := uc.Release(context.Background(), allocationID)
	assert.ErrorIs(t, err, inbound.ErrNotFound)
}

// TC-QUE-37 (1.4, P3): the internal release ends the admission as UNCLAIMED; a locked (EDITABLE) admission
// is reported as released=false so the internal contract is unchanged.
func TestReleaseAllocation_ReasonAndLocked(t *testing.T) {
	t.Run("passes UNCLAIMED", func(t *testing.T) {
		repo := &fakeAdmissionRepo{}
		_, err := usecase.NewReleaseAllocation(repo).Release(context.Background(), allocationID)
		require.NoError(t, err)
		assert.Equal(t, []vo.EndReason{vo.EndUnclaimed}, repo.releaseReasons)
	})
	t.Run("ErrBookingLocked maps to released=false", func(t *testing.T) {
		repo := &fakeAdmissionRepo{releaseErr: inbound.ErrBookingLocked}
		resp, err := usecase.NewReleaseAllocation(repo).Release(context.Background(), allocationID)
		require.NoError(t, err)
		assert.False(t, resp.Released)
	})
}
