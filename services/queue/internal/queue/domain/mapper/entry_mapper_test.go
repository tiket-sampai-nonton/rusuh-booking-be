package mapper_test

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"

	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/queue/internal/queue/domain/entity"
	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/queue/internal/queue/domain/mapper"
	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/queue/internal/queue/domain/valueobject"
)

func TestToQueueEntry(t *testing.T) {
	now := time.Date(2026, 10, 20, 10, 0, 0, 0, time.UTC)
	joined := time.Date(2026, 10, 20, 9, 59, 0, 0, time.UTC).UnixMilli()
	e := entity.Entry{ID: "e1", Seq: valueobject.Seq(42), Status: valueobject.StatusWaiting, JoinedAtMs: joined}

	got := mapper.ToQueueEntry(e, now)
	assert.Equal(t, "e1", got.EntryID)
	assert.Equal(t, int64(42), got.Seq)
	assert.Equal(t, "WAITING", got.Status)
	assert.Equal(t, "2026-10-20T09:59:00Z", got.JoinedAt)
	assert.Equal(t, "2026-10-20T10:00:00Z", got.ServerTime)
	assert.Empty(t, got.ClaimDeadline, "no deadline while waiting")
}

func TestToQueueEntry_Admitted(t *testing.T) {
	now := time.Date(2026, 10, 20, 10, 0, 0, 0, time.UTC)
	dl := now.Add(time.Minute).UnixMilli()
	e := entity.Entry{ID: "e2", Seq: valueobject.Seq(1), Status: valueobject.StatusAdmitted, ClaimDeadlineMs: dl}
	got := mapper.ToQueueEntry(e, now)
	assert.Equal(t, "ADMITTED", got.Status)
	assert.Equal(t, time.UnixMilli(dl).UTC().Format(time.RFC3339), got.ClaimDeadline)
}

func TestToEntryStatus_StatesAndNullRank(t *testing.T) {
	tests := []struct {
		status valueobject.Status
		state  string
		rank   bool
	}{
		{valueobject.StatusWaiting, "WAITING", true},
		{valueobject.StatusStale, "STALE", true},
		{valueobject.StatusAdmitted, "ADMITTED", true},
		{valueobject.StatusLeft, "ENDED", false},
		{valueobject.StatusCompleted, "ENDED", false},
	}
	for _, tt := range tests {
		t.Run(string(tt.status), func(t *testing.T) {
			got := mapper.ToEntryStatus(entity.Entry{ID: "e", Seq: 3, Status: tt.status})
			assert.Equal(t, tt.state, got.State)
			assert.Equal(t, tt.rank, got.Rank != nil)
			if tt.rank {
				assert.Equal(t, int64(3), *got.Rank, "rank is the seq (strict FIFO)")
			}
			assert.Nil(t, got.Zone, "zone is added by the usecase")
		})
	}
}
