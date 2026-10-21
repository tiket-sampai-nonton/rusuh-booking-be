package worker

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/tiket-sampai-nonton/rusuh-booking-be/pkg/clock"

	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/queue/internal/queue/domain/dto"
	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/queue/internal/queue/domain/entity"
	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/queue/internal/queue/port/outbound"
)

// StatusPublisher recomputes and caches the public Status Document for every event (design §7.2).
type StatusPublisher struct {
	store    outbound.IQueueStore
	settings outbound.IEventSettingsRepo
	booking  outbound.IBookingClient
	clock    clock.IClock
	ttl      time.Duration
}

// NewStatusPublisher accepts interfaces and returns the concrete worker. ttl is the cache TTL of the
// status key (design §3.1: ~1s).
func NewStatusPublisher(store outbound.IQueueStore, settings outbound.IEventSettingsRepo, booking outbound.IBookingClient, clk clock.IClock, ttl time.Duration) *StatusPublisher {
	if ttl <= 0 {
		ttl = time.Second
	}
	return &StatusPublisher{store: store, settings: settings, booking: booking, clock: clk, ttl: ttl}
}

// Run publishes one document per configured event.
func (p *StatusPublisher) Run(ctx context.Context) error {
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

func (p *StatusPublisher) runEvent(ctx context.Context, eventID string) error {
	settings, ok, err := p.settings.Get(ctx, eventID)
	if err != nil || !ok {
		return err
	}
	sale, err := p.store.GetSale(ctx, eventID)
	if err != nil {
		return err
	}
	supply, serr := p.booking.Supply(ctx, eventID) // best-effort; on error we publish with zero stock

	doc := p.build(eventID, settings, sale, supply, serr == nil)
	raw, err := json.Marshal(doc)
	if err != nil {
		return fmt.Errorf("status publisher: marshal: %w", err)
	}
	return p.store.SetStatusDoc(ctx, eventID, raw, p.ttl)
}

func (p *StatusPublisher) build(eventID string, s entity.EventSettings, sale outbound.SaleState, supply outbound.SupplySnapshot, haveSupply bool) dto.StatusDocument {
	now := p.clock.Now().UTC()
	saleState := sale.SaleState
	if saleState == "" {
		saleState = dto.SaleOpen
	}
	remaining := 0
	soldOut := sale.SoldOut
	if haveSupply {
		remaining = supply.RemainingTickets
		soldOut = soldOut || supply.SoldOut
	}
	// Guaranteed covers the next `remaining` seqs; possible extends by near_front_multiplier.
	guaranteedUntil := sale.ServingSeq + int64(remaining)
	possibleUntil := sale.ServingSeq + int64(remaining*maxInt(1, s.NearFrontMultiplier))

	return dto.StatusDocument{
		EventID:               eventID,
		SaleState:             saleState,
		ServingSeq:            sale.ServingSeq,
		GuaranteedUntilSeq:    guaranteedUntil,
		PossibleUntilSeq:      possibleUntil,
		SoldOut:               soldOut,
		RemainingTickets:      remaining,
		NextPollAfterMs:       s.PollNearMs,
		Poll:                  dto.Poll{FarMs: s.PollFarMs, MidMs: s.PollMidMs, NearMs: s.PollNearMs, FarAbove: 500, MidAbove: 100},
		PreTurnNotifyPosition: s.NearFrontMultiplier * s.ActiveSlotCapacity,
		Categories:            categoriesFrom(supply, s.CategoryLimitedThresholdPct, haveSupply),
		UpdatedAt:             now.Format(time.RFC3339),
	}
}

func categoriesFrom(supply outbound.SupplySnapshot, limitedPct int, haveSupply bool) []dto.CategoryState {
	if !haveSupply || len(supply.Categories) == 0 {
		return nil
	}
	out := make([]dto.CategoryState, 0, len(supply.Categories))
	for _, c := range supply.Categories {
		state := dto.CategoryAvailable
		switch {
		case c.Total <= 0 || c.Sold >= c.Total:
			state = dto.CategorySoldOut
		case (c.Total-c.Sold)*100 <= c.Total*limitedPct:
			state = dto.CategoryLimited
		}
		out = append(out, dto.CategoryState{Code: c.Code, Name: c.Name, State: state})
	}
	return out
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}
