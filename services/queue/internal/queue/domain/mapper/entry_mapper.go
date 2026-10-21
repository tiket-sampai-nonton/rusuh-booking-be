package mapper

import (
	"time"

	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/queue/internal/queue/domain/dto"
	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/queue/internal/queue/domain/entity"
	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/queue/internal/queue/domain/valueobject"
)

// msToRFC3339 formats an epoch-millis timestamp as RFC3339 (UTC). Zero → "".
func msToRFC3339(ms int64) string {
	if ms <= 0 {
		return ""
	}
	return time.UnixMilli(ms).UTC().Format(time.RFC3339)
}

// ToQueueEntry maps a freshly-created Entry to the Join Queue 201 DTO. Tokens/URLs are filled by the
// usecase (they depend on signing, not on the entity).
func ToQueueEntry(e entity.Entry, now time.Time) dto.QueueEntry {
	return dto.QueueEntry{
		EntryID:       e.ID,
		Seq:           e.Seq.Int64(),
		Status:        string(e.Status),
		ClaimDeadline: msToRFC3339(e.ClaimDeadlineMs),
		JoinedAt:      msToRFC3339(e.JoinedAtMs),
		ServerTime:    now.UTC().Format(time.RFC3339),
	}
}

// ToEntryStatus maps an Entry plus serving context to the My Entry DTO (design §2.3).
// exactPosition = seq - servingSeq - removedAhead; zone from the guaranteed/possible boundaries.
func ToEntryStatus(e entity.Entry, servingSeq, removedAhead, guaranteedUntil, possibleUntil int64) dto.EntryStatus {
	status := dto.EntryStatus{
		EntryID:       e.ID,
		Seq:           e.Seq.Int64(),
		Status:        string(e.Status),
		ClaimDeadline: msToRFC3339(e.ClaimDeadlineMs),
	}
	if e.Status == valueobject.StatusWaiting {
		pos := e.ExactPosition(servingSeq, removedAhead)
		if pos < 0 {
			pos = 0
		}
		status.ExactPosition = pos
		status.Zone = string(valueobject.ZoneFor(e.Seq, valueobject.Seq(guaranteedUntil), valueobject.Seq(possibleUntil)))
	}
	return status
}
