// Package compensation holds the pure decision of when the Queue may release a Booking allocation after a
// reserve-then-promote that did not end in ADMITTED (design §5.4). It defines no interface: it depends on the
// outbound port and is shared by the join usecase and the promoter.
package compensation

import (
	"context"
	"log/slog"

	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/queue/internal/queue/port/outbound"
)

// ReleaseReason is the reason sent to Booking with a release.
const ReleaseReason = "QUEUE_ENTRY_GONE"

// ShouldRelease reports whether the allocation granted for an entry must be released, given the status that
// promote.lua reported for it atomically with its decision. Only provably terminal entries qualify: releasing a
// RESERVED row turns it into RELEASED at Booking, after which the same entry can never be granted again
// (ALREADY_ACTIVE), so a release for an entry that is merely deferred would kill a live buyer.
func ShouldRelease(skipStatus string) bool {
	switch skipStatus {
	case "NO_ENTRY", "LEFT", "ENDED", "COMPLETED", "MISSED", "ADMITTED_OTHER":
		return true
	}
	return false
}

// ReleaseTerminal releases the granted allocation of every skip that ShouldRelease approves and returns how many
// releases succeeded. granted maps entryID to the allocation id Booking granted in this attempt: the granted id is
// released, never the one stored on the entry. A failed release is logged (ids only, no PII) and falls back to
// Booking's session sweeper; it never stops the next skip.
func ReleaseTerminal(ctx context.Context, booking outbound.IBookingClient, logger *slog.Logger, skipped []outbound.PromoteSkip, granted map[string]string) int {
	if logger == nil {
		logger = slog.Default()
	}
	released := 0
	for _, sk := range skipped {
		if !ShouldRelease(sk.Status) {
			continue
		}
		allocID := granted[sk.EntryID]
		if allocID == "" {
			continue
		}
		if err := booking.ReleaseAllocation(ctx, allocID, ReleaseReason); err != nil {
			logger.Warn("alokasi entry terminal tidak bisa dilepas; sweeper Booking akan melepasnya",
				"entryId", sk.EntryID, "allocationId", allocID, "status", sk.Status, "error", err)
			continue
		}
		released++
	}
	return released
}
