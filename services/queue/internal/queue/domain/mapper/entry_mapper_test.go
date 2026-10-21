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

func TestToEntryStatus_WaitingComputesPositionAndZone(t *testing.T) {
	e := entity.Entry{ID: "e1", Seq: valueobject.Seq(100), Status: valueobject.StatusWaiting}
	got := mapper.ToEntryStatus(e, 55, 5, 150, 200)
	assert.Equal(t, int64(40), got.ExactPosition) // 100-55-5
	assert.Equal(t, "GUARANTEED", got.Zone)
}

func TestToEntryStatus_NegativePositionClampedToZero(t *testing.T) {
	e := entity.Entry{ID: "e1", Seq: valueobject.Seq(50), Status: valueobject.StatusWaiting}
	got := mapper.ToEntryStatus(e, 60, 0, 150, 200)
	assert.Equal(t, int64(0), got.ExactPosition)
}

func TestToEntryStatus_AdmittedNoPositionNoZone(t *testing.T) {
	e := entity.Entry{ID: "e1", Seq: valueobject.Seq(10), Status: valueobject.StatusAdmitted}
	got := mapper.ToEntryStatus(e, 5, 0, 150, 200)
	assert.Equal(t, int64(0), got.ExactPosition)
	assert.Empty(t, got.Zone)
}
