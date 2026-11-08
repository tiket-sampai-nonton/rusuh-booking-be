package usecase_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	mock_outbound "github.com/tiket-sampai-nonton/rusuh-booking-be/services/queue/_mock/queue/port/outbound"
	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/queue/internal/queue/application/usecase"
	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/queue/internal/queue/domain/entity"
	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/queue/internal/queue/port/inbound"
	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/queue/internal/queue/port/outbound"
)

// TC-QUE-38 (P16, P17): DELETE on a CLAIMED entry is refused with ErrEntryClaimed and releases nothing
// (Booking owns the session once claimed); every other state keeps its behaviour.
func TestLeave_ByStatus(t *testing.T) {
	tests := []struct {
		name      string
		res       outbound.LeaveResult
		storeErr  error
		wantErr   error
		wantRelID string // non-empty: Booking.ReleaseAllocation must be called with it
	}{
		{name: "claimed is refused, nothing released", res: outbound.LeaveResult{Tag: "IGNORED", PrevStatus: "CLAIMED", AllocationID: "alloc-1"}, wantErr: entity.ErrEntryClaimed},
		{name: "waiting leaves", res: outbound.LeaveResult{Tag: "OK", PrevStatus: "WAITING"}},
		{name: "admitted leaves and releases", res: outbound.LeaveResult{Tag: "OK", PrevStatus: "ADMITTED", AllocationID: "alloc-1"}, wantRelID: "alloc-1"},
		{name: "completed is idempotent", res: outbound.LeaveResult{Tag: "IGNORED", PrevStatus: "COMPLETED"}},
		{name: "ended is idempotent", res: outbound.LeaveResult{Tag: "IGNORED", PrevStatus: "ENDED"}},
		{name: "left is idempotent", res: outbound.LeaveResult{Tag: "IGNORED", PrevStatus: "LEFT"}},
		{name: "missed is idempotent", res: outbound.LeaveResult{Tag: "IGNORED", PrevStatus: "MISSED"}},
		{name: "stranger is not found", storeErr: entity.ErrNotFound, wantErr: entity.ErrNotFound},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctrl := gomock.NewController(t)
			store := mock_outbound.NewMockIQueueStore(ctrl)
			booking := mock_outbound.NewMockIBookingClient(ctrl) // an unexpected ReleaseAllocation fails the test
			store.EXPECT().TokenGen(gomock.Any(), "ev1", gomock.Any()).Return(int64(1), true, nil).AnyTimes()
			store.EXPECT().Leave(gomock.Any(), "ev1", "e1", "v1", gomock.Any()).Return(tt.res, tt.storeErr)
			if tt.wantRelID != "" {
				booking.EXPECT().ReleaseAllocation(gomock.Any(), tt.wantRelID, outbound.ReleaseReasonEntryGone).Return(nil)
			}
			err := usecase.NewLeaveQueue(store, booking, fixedNow).Leave(context.Background(),
				inbound.LeaveInput{EntryID: "e1", VisitorID: "v1", EventID: "ev1", Gen: 1})
			if tt.wantErr != nil {
				assert.ErrorIs(t, err, tt.wantErr)
				return
			}
			require.NoError(t, err)
		})
	}
}
