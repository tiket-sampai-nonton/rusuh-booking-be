package worker

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/tiket-sampai-nonton/rusuh-booking-be/pkg/clock"

	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/queue/internal/queue/domain/dto"
	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/queue/internal/queue/domain/entity"
	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/queue/internal/queue/domain/valueobject"
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
	var errs []error
	for _, eventID := range ids {
		if err := p.runEvent(ctx, eventID); err != nil {
			errs = append(errs, fmt.Errorf("status publisher event %s: %w", eventID, err))
		}
	}
	return errors.Join(errs...)
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
	supply, serr := p.booking.Supply(ctx, eventID) // best-effort; on error the previous boundaries are kept

	doc := p.build(eventID, settings, sale, supply, serr == nil)
	if err := p.ranks(ctx, eventID, settings, supply, serr == nil, &doc); err != nil {
		return err
	}
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
	return dto.StatusDocument{
		EventID:               eventID,
		SaleState:             saleState,
		ServingSeq:            sale.ServingSeq,
		SoldOut:               soldOut,
		RemainingTickets:      remaining,
		NextPollAfterMs:       s.PollNearMs,
		Poll:                  dto.Poll{FarMs: s.PollFarMs, MidMs: s.PollMidMs, NearMs: s.PollNearMs, FarAbove: 500, MidAbove: 100},
		PreTurnNotifyPosition: s.NearFrontMultiplier * s.ActiveSlotCapacity,
		Categories:            categoriesFrom(supply, s.CategoryLimitedThresholdPct, haveSupply),
		UpdatedAt:             now.Format(time.RFC3339),
	}
}

// ranks fills the seq-based zone fields (ADR-030, R05.1): G = ceil(F/M) and P = ceil((F+A+H)/M) front-most LIVE
// entries (STALE are not in the waiting set), boundary = seq of the G-th / P-th live entry (the last live seq when
// fewer live entries exist), 0 for an empty zone. All joins are strict FIFO by seq (ADR-030 Amendment: the
// opening-window draw was dropped).
func (p *StatusPublisher) ranks(ctx context.Context, eventID string, s entity.EventSettings, supply outbound.SupplySnapshot, haveSupply bool, doc *dto.StatusDocument) error {
	count, err := p.store.WaitingCount(ctx, eventID)
	if err != nil {
		return err
	}
	seqOfNth := func(n int64) (*int64, error) {
		var r int64
		if n > 0 && count > 0 {
			got, _, err := p.store.WaitingSeqAt(ctx, eventID, min(n, count)-1)
			if err != nil {
				return nil, err
			}
			r = got
		}
		return &r, nil
	}
	if doc.HeadSeq, err = seqOfNth(1); err != nil {
		return err
	}
	if !haveSupply {
		// Booking unavailable: keep the last published boundaries instead of telling everyone "not enough".
		if raw, ok, gerr := p.store.GetStatusDoc(ctx, eventID); gerr == nil && ok {
			var prev dto.StatusDocument
			if json.Unmarshal(raw, &prev) == nil {
				doc.GuaranteedUntilSeq, doc.PossibleUntilSeq = prev.GuaranteedUntilSeq, prev.PossibleUntilSeq
			}
		}
		return nil
	}
	g, pp := valueobject.ZoneCounts(supply.FreeUnits, supply.AllocatedUnits, supply.HeldUnits, supply.MaxTicketsPerOrder)
	if doc.GuaranteedUntilSeq, err = seqOfNth(g); err != nil {
		return err
	}
	doc.PossibleUntilSeq, err = seqOfNth(pp)
	return err
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
