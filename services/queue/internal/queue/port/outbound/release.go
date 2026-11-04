package outbound

import (
	"context"
	"log/slog"

	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/queue/internal/queue/domain/valueobject"
)

// ReleaseReasonEntryGone is the reason sent to Booking when the Queue releases an allocation because the entry is gone.
const ReleaseReasonEntryGone = "QUEUE_ENTRY_GONE"

// ReleaseTerminal releases the granted allocation of every skip that valueobject.ShouldRelease approves and returns
// how many releases succeeded. granted maps entryID to the allocation id Booking granted in this attempt: the granted
// id is released, never the one stored on the entry. A failed release is logged (ids only, no PII) and falls back to
// Booking's session sweeper; it never stops the next skip. It lives next to the port so that both the join usecase and
// the promoter adapter can share it without the adapter importing the application layer.
func ReleaseTerminal(ctx context.Context, booking IBookingClient, logger *slog.Logger, skipped []PromoteSkip, granted map[string]string) int {
	if logger == nil {
		logger = slog.Default()
	}
	released := 0
	for _, sk := range skipped {
		if !valueobject.ShouldRelease(sk.Status) {
			continue
		}
		allocID := granted[sk.EntryID]
		if allocID == "" {
			continue
		}
		if err := booking.ReleaseAllocation(ctx, allocID, ReleaseReasonEntryGone); err != nil {
			logger.Warn("alokasi entry terminal tidak bisa dilepas; sweeper Booking akan melepasnya",
				"entryId", sk.EntryID, "allocationId", allocID, "status", sk.Status, "error", err)
			continue
		}
		released++
	}
	return released
}
