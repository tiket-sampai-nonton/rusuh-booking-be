package mapper

import (
	"time"

	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/queue/internal/queue/domain/dto"
	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/queue/internal/queue/domain/entity"
)

// msToRFC3339 formats an epoch-millis timestamp as RFC3339 (UTC). Zero → "".
func msToRFC3339(ms int64) string {
	if ms <= 0 {
		return ""
	}
	return time.UnixMilli(ms).UTC().Format(time.RFC3339)
}

// ToQueueEntry maps an Entry to the Join Queue DTO. Tokens/URLs are filled by the usecase.
func ToQueueEntry(e entity.Entry, now time.Time) dto.QueueEntry {
	return dto.QueueEntry{
		EntryID:       e.ID,
		Seq:           e.Seq.Int64(),
		Status:        string(e.Status),
		ClaimDeadline: msToRFC3339(e.ClaimDeadlineMs),
		JoinedAt:      msToRFC3339(e.JoinedAtMs),
		ServerTime:    now.UTC().Format(time.RFC3339),
		State:         StateOf(e),
		Rank:          rankOf(e),
	}
}

// ToEntryStatus maps an Entry to the My Entry DTO without position context (the usecase adds position, zone and
// interval). Rank (= seq, strict FIFO) is null once ENDED.
func ToEntryStatus(e entity.Entry) dto.EntryStatus {
	return dto.EntryStatus{
		EntryID:       e.ID,
		Seq:           e.Seq.Int64(),
		Status:        string(e.Status),
		State:         StateOf(e),
		Rank:          rankOf(e),
		ClaimDeadline: msToRFC3339(e.ClaimDeadlineMs),
	}
}

// StateOf maps the stored status to the client state (ADR-030): terminal statuses are ENDED; the rest pass through.
func StateOf(e entity.Entry) string {
	if e.Status.IsTerminal() {
		return "ENDED"
	}
	return string(e.Status)
}

// rankOf is the admission order (= seq, strict FIFO by seq; ADR-030 Amendment); null for ended entries.
func rankOf(e entity.Entry) *int64 {
	if e.Status.IsTerminal() || e.Seq.Int64() <= 0 {
		return nil
	}
	r := e.Seq.Int64()
	return &r
}
