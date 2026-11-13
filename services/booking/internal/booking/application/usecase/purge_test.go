package usecase_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	"github.com/tiket-sampai-nonton/rusuh-booking-be/pkg/clock"
	mock_outbound "github.com/tiket-sampai-nonton/rusuh-booking-be/services/booking/_mock/booking/port/outbound"
	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/booking/internal/booking/application/usecase"
)

// batchesFrom returns the batch size of call i (0-based); past the end it repeats the last size.
func batchesFrom(sizes []int) func() int {
	i := 0
	return func() int {
		n := sizes[len(sizes)-1]
		if i < len(sizes) {
			n = sizes[i]
		}
		i++
		return n
	}
}

// D-C7.8: each table is drained in batches of 1000 and repeated while a batch comes back full, at most 20 rounds.
func TestPurge_RepeatsWhileBatchFull(t *testing.T) {
	tests := []struct {
		name      string
		batches   []int
		wantCalls int
	}{
		{"short first batch ends the table at once", []int{3}, 1},
		{"empty table is one call", []int{0}, 1},
		{"full batches repeat until a short one", []int{1000, 1000, 7}, 3},
		{"more than 20 full rounds stop at the round cap", []int{1000}, 20},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctrl := gomock.NewController(t)
			ret := mock_outbound.NewMockIRetention(ctrl)
			now := time.Date(2026, 11, 13, 12, 0, 0, 0, time.UTC)

			var calls [3]int
			next := [3]func() int{batchesFrom(tt.batches), batchesFrom(tt.batches), batchesFrom(tt.batches)}
			ret.EXPECT().PurgePublishedOutbox(gomock.Any(), gomock.Any(), 1000).
				DoAndReturn(func(_ context.Context, _ time.Time, _ int) (int, error) { calls[0]++; return next[0](), nil }).AnyTimes()
			ret.EXPECT().PurgeExpiredIdempotency(gomock.Any(), gomock.Any(), 1000).
				DoAndReturn(func(_ context.Context, _ time.Time, _ int) (int, error) { calls[1]++; return next[1](), nil }).AnyTimes()
			ret.EXPECT().PurgeAttendeeChecks(gomock.Any(), gomock.Any(), 1000).
				DoAndReturn(func(_ context.Context, _ time.Time, _ int) (int, error) { calls[2]++; return next[2](), nil }).AnyTimes()

			require.NoError(t, usecase.NewPurge(ret, clock.Fixed{T: now}).Run(context.Background()))
			assert.Equal(t, [3]int{tt.wantCalls, tt.wantCalls, tt.wantCalls}, calls, "each table drains on its own")
		})
	}
}

// One table failing does not stop the other two; the error is returned after all three were tried.
func TestPurge_ErrorInOneTableDoesNotStopOthers(t *testing.T) {
	ctrl := gomock.NewController(t)
	ret := mock_outbound.NewMockIRetention(ctrl)
	boom := errors.New("boom")
	ret.EXPECT().PurgePublishedOutbox(gomock.Any(), gomock.Any(), 1000).Return(0, boom)
	ret.EXPECT().PurgeExpiredIdempotency(gomock.Any(), gomock.Any(), 1000).Return(0, nil)
	ret.EXPECT().PurgeAttendeeChecks(gomock.Any(), gomock.Any(), 1000).Return(0, nil)

	err := usecase.NewPurge(ret, clock.Fixed{T: time.Now()}).Run(context.Background())
	assert.ErrorIs(t, err, boom)
}
