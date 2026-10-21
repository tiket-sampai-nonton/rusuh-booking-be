// Package worker holds the Queue background jobs: Promotor, Sweeper, Status Publisher (design §7).
// Each Run is idempotent per tick so it can be driven by the shared worker loop.
package worker

import (
	"context"
	"log/slog"
	"time"

	"github.com/tiket-sampai-nonton/rusuh-booking-be/pkg/clock"

	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/queue/internal/queue/port/outbound"
)

// Promoter promotes waiting entries to active after Booking reserves their allocation (design §7.1).
// It never admits an entry that is not RESERVED ("Jangan PHP" invariant).
type Promoter struct {
	store    outbound.IQueueStore
	settings outbound.IEventSettingsRepo
	booking  outbound.IBookingClient
	pub      outbound.IEventPublisher
	audit    outbound.IEntryAuditRepo
	clock    clock.IClock
	logger   *slog.Logger
	batchMax int
}

// NewPromoter accepts interfaces and returns the concrete worker.
func NewPromoter(store outbound.IQueueStore, settings outbound.IEventSettingsRepo, booking outbound.IBookingClient, pub outbound.IEventPublisher, audit outbound.IEntryAuditRepo, clk clock.IClock, logger *slog.Logger, batchMax int) *Promoter {
	if batchMax <= 0 {
		batchMax = 200
	}
	if logger == nil {
		logger = slog.Default()
	}
	return &Promoter{store: store, settings: settings, booking: booking, pub: pub, audit: audit, clock: clk, logger: logger, batchMax: batchMax}
}

// Run promotes one batch for every configured event.
func (p *Promoter) Run(ctx context.Context) error {
	ids, err := p.settings.ListEventIDs(ctx)
	if err != nil {
		return err
	}
	for _, eventID := range ids {
		if err := p.runEvent(ctx, eventID); err != nil {
			return err
		}
	}
	return nil
}

func (p *Promoter) runEvent(ctx context.Context, eventID string) error {
	settings, ok, err := p.settings.Get(ctx, eventID)
	if err != nil || !ok {
		return err
	}
	active, err := p.store.ActiveCount(ctx, eventID)
	if err != nil {
		return err
	}
	free := settings.ActiveSlotCapacity - active
	if free <= 0 {
		// No slots to fill, but still check pre-turn notifications.
		if settings.PreTurnNotifyEnabled && settings.NearFrontMultiplier > 0 {
			p.notifyPreTurn(ctx, eventID, settings.ActiveSlotCapacity*settings.NearFrontMultiplier)
		}
		return nil
	}
	if free > p.batchMax {
		free = p.batchMax
	}
	candidates, err := p.store.TopWaiting(ctx, eventID, free)
	if err != nil || len(candidates) == 0 {
		return err
	}

	now := p.clock.Now()
	entries := make([]outbound.AllocationEntryRequest, 0, len(candidates))
	visitorByEntry := map[string]string{}
	for _, entryID := range candidates {
		ent, found, gerr := p.store.GetEntry(ctx, eventID, entryID)
		if gerr != nil || !found {
			continue
		}
		visitorByEntry[entryID] = ent.VisitorID
		entries = append(entries, outbound.AllocationEntryRequest{
			QueueEntryID: entryID, VisitorID: ent.VisitorID, BuyerNikHash: ent.BuyerNikHashHex(), Units: 1,
			Seq: int64(ent.Seq),
		})
	}
	if len(entries) == 0 {
		return nil
	}

	resp, err := p.booking.ReserveBatch(ctx, outbound.AllocationBatchRequest{EventID: eventID, Entries: entries})
	if err != nil {
		return err // Booking down — leave entries waiting, retry next tick
	}

	var promoteCands []outbound.PromoteCandidate
	for _, a := range resp.Allocations {
		switch a.Status {
		case "GRANTED": // Booking confirmed allocation — promote entry to ADMITTED
			deadline := now.Add(time.Minute)
			if dl, perr := time.Parse(time.RFC3339, a.ClaimDeadline); perr == nil {
				deadline = dl
			}
			promoteCands = append(promoteCands, outbound.PromoteCandidate{
				EntryID: a.QueueEntryID, AllocationID: a.AllocationID, ClaimDeadlineMs: deadline.UnixMilli(),
			})
		case "BUYER_QUOTA_EXHAUSTED", "QUOTA_EXHAUSTED":
			if ent, found, gerr := p.store.GetEntry(ctx, eventID, a.QueueEntryID); gerr == nil && found {
				_ = p.store.AddQuotaExhausted(ctx, eventID, ent.BuyerNikHashHex())
			}
		case "SOLD_OUT":
			_ = p.store.SetSale(ctx, eventID, "CLOSED", true)
		case "ALREADY_ACTIVE":
			// Bug indicator: Queue constraints should prevent this. End entry and log for investigation (OQ-18).
			if _, err := p.store.EndEntry(ctx, eventID, a.QueueEntryID, "ALREADY_ACTIVE", now.UnixMilli()); err != nil {
				p.logger.Warn("failed to end ALREADY_ACTIVE entry", "error", err)
			}
			p.logger.Warn("ALREADY_ACTIVE dari Booking — kemungkinan bug constraint antrean",
				"eventId", eventID, "queueEntryId", a.QueueEntryID)
		}
	}
	if len(promoteCands) == 0 {
		return nil
	}

	promoted, err := p.store.Promote(ctx, eventID, promoteCands, settings.ActiveSlotCapacity, now.UnixMilli())
	if err != nil {
		return err
	}
	deadlineByEntry := map[string]int64{}
	for _, c := range promoteCands {
		deadlineByEntry[c.EntryID] = c.ClaimDeadlineMs
	}
	for _, entryID := range promoted {
		_ = p.pub.PublishTurnArrived(ctx, eventID, entryID, visitorByEntry[entryID], time.UnixMilli(deadlineByEntry[entryID]).UTC())
	}
	// Always check near-front entries for pre-turn notifications (TC-QUE-13).
	if settings.PreTurnNotifyEnabled && settings.NearFrontMultiplier > 0 {
		p.notifyPreTurn(ctx, eventID, settings.ActiveSlotCapacity*settings.NearFrontMultiplier)
	}
	return nil
}

// notifyPreTurn publishes queue.turn.soon for waiting entries near the front (TC-QUE-13, SRS-F-25).
func (p *Promoter) notifyPreTurn(ctx context.Context, eventID string, notifyPosition int) {
	if notifyPosition <= 0 {
		return
	}
	nearFront, err := p.store.TopWaiting(ctx, eventID, notifyPosition)
	if err != nil || len(nearFront) == 0 {
		return
	}
	for _, entryID := range nearFront {
		notified, err := p.store.WasNotifiedSoon(ctx, eventID, entryID)
		if err != nil || notified {
			continue
		}
		ent, found, err := p.store.GetEntry(ctx, eventID, entryID)
		if err != nil || !found {
			continue
		}
		if pubErr := p.pub.PublishTurnSoon(ctx, eventID, entryID, ent.VisitorID); pubErr == nil {
			_ = p.store.MarkNotifiedSoon(ctx, eventID, entryID)
		}
	}
}
