package worker

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"sync"
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

	windowMu sync.Mutex          // guards windows
	windows  map[string][2]int64 // sale window (starts, ends in ms) last written to event_settings, per event
}

// NewStatusPublisher accepts interfaces and returns the concrete worker. ttl is the cache TTL of the
// status key (design §3.1: ~1s).
func NewStatusPublisher(store outbound.IQueueStore, settings outbound.IEventSettingsRepo, booking outbound.IBookingClient, clk clock.IClock, ttl time.Duration) *StatusPublisher {
	if ttl <= 0 {
		ttl = time.Second
	}
	return &StatusPublisher{store: store, settings: settings, booking: booking, clock: clk, ttl: ttl, windows: map[string][2]int64{}}
}

// Run publishes one document per configured event.
func (p *StatusPublisher) Run(ctx context.Context) error {
	ids, err := p.settings.ListActiveEventIDs(ctx, p.clock.Now())
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
	if serr == nil {
		p.cacheSaleWindow(ctx, eventID, supply)
		p.persistSaleWindow(ctx, eventID, supply)
		if p.reopenable(sale, supply) {
			if err := p.store.SetSale(ctx, eventID, dto.SaleOpen, false); err != nil {
				return fmt.Errorf("status publisher: reopen sale: %w", err)
			}
			sale.SaleState, sale.SoldOut = dto.SaleOpen, false
		}
	}

	doc := p.build(eventID, settings, sale, supply, serr == nil)
	rankErr := p.ranks(ctx, eventID, settings, supply, serr == nil, &doc)
	if rankErr != nil {
		// The zone fields could not be read: publish the last known ones instead of an empty zone (P3-15), then
		// report the failure.
		p.keepPrevious(ctx, eventID, &doc)
	}
	raw, err := json.Marshal(doc)
	if err != nil {
		return errors.Join(rankErr, fmt.Errorf("status publisher: marshal: %w", err))
	}
	if err := p.store.SetStatusDoc(ctx, eventID, raw, p.ttl); err != nil {
		return errors.Join(rankErr, err)
	}
	return errors.Join(rankErr, p.retireIfEnded(ctx, eventID, supply, serr == nil))
}

// retireIfEnded gives a finished event's fixed keys a TTL (D-C8.2, P3-13) once its sale has ended. Whether anything is
// still live is decided inside RetireEvent's atomic script, so a join cannot land between a check and the TTL. Ended
// entries are not live; the write that ended each one set its own TTL (end_entry.lua, liveness.lua, ...). It runs on
// every tick and RetireEvent is idempotent. Without a readable Booking supply the sale end is unknown, so nothing runs.
func (p *StatusPublisher) retireIfEnded(ctx context.Context, eventID string, supply outbound.SupplySnapshot, haveSupply bool) error {
	if !haveSupply || supply.SalesEndsAt.IsZero() || p.clock.Now().Before(supply.SalesEndsAt) {
		return nil
	}
	_, err := p.store.RetireEvent(ctx, eventID, outbound.EntryRetention)
	return err
}

// cacheSaleWindow stores Booking's sale window for the join gate. Best-effort: a failed write is logged and the
// document is still published (the gate falls back to one Booking read when the window is unknown).
func (p *StatusPublisher) cacheSaleWindow(ctx context.Context, eventID string, supply outbound.SupplySnapshot) {
	if supply.SalesStartsAt.IsZero() || supply.SalesEndsAt.IsZero() {
		return
	}
	if err := p.store.SetSaleWindow(ctx, eventID, supply.SalesStartsAt.UnixMilli(), supply.SalesEndsAt.UnixMilli()); err != nil {
		slog.Warn("status publisher: cache sale window failed", "eventId", eventID, "error", err)
	}
}

// persistSaleWindow stores Booking's sale window in event_settings, which decides how long the workers iterate the event
// (D-C8.10). It writes only when the window differs from the one this process last wrote. A failed write is logged and not
// remembered, so the next tick retries it. A zero window is not written: the event then stays iterated.
func (p *StatusPublisher) persistSaleWindow(ctx context.Context, eventID string, supply outbound.SupplySnapshot) {
	if supply.SalesStartsAt.IsZero() || supply.SalesEndsAt.IsZero() {
		return
	}
	if !supply.SalesEndsAt.After(supply.SalesStartsAt) {
		// A window that ends before it starts would drop the event from the iteration for good (R40): not written, not remembered.
		slog.Warn("status publisher: sale window rejected, ends not after starts", "eventId", eventID,
			"startsAt", supply.SalesStartsAt, "endsAt", supply.SalesEndsAt)
		return
	}
	window := [2]int64{supply.SalesStartsAt.UnixMilli(), supply.SalesEndsAt.UnixMilli()}
	p.windowMu.Lock()
	defer p.windowMu.Unlock()
	if last, ok := p.windows[eventID]; ok && last == window {
		return
	}
	if err := p.settings.UpdateSaleWindow(ctx, eventID, supply.SalesStartsAt, supply.SalesEndsAt); err != nil {
		slog.Warn("status publisher: write sale window failed", "eventId", eventID, "error", err)
		return
	}
	p.windows[eventID] = window
}

// reopenable reports whether a sold-out sale should reopen (D-C5.3): Booking has free stock again and now is inside
// the sale window. A cancelled event (Booking reports soldOut) or an ended sale stays closed; a zero window never
// matches, so an unknown window does not reopen anything.
func (p *StatusPublisher) reopenable(sale outbound.SaleState, supply outbound.SupplySnapshot) bool {
	now := p.clock.Now()
	inWindow := !now.Before(supply.SalesStartsAt) && now.Before(supply.SalesEndsAt)
	return sale.SoldOut && !supply.SoldOut && supply.RemainingTickets > 0 && inWindow
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
		SoldOut:               soldOut,
		RemainingTickets:      remaining,
		NextPollAfterMs:       s.PollNearMs,
		Poll:                  dto.Poll{FarMs: s.PollFarMs, MidMs: s.PollMidMs, NearMs: s.PollNearMs, FarAbove: s.PollFarAbove, MidAbove: s.PollMidAbove},
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
		p.keepPrevious(ctx, eventID, doc)
		return nil
	}
	g, pp := valueobject.ZoneCounts(supply.FreeUnits, supply.AllocatedUnits, supply.HeldUnits, supply.MaxTicketsPerOrder)
	if doc.GuaranteedUntilSeq, err = seqOfNth(g); err != nil {
		return err
	}
	doc.PossibleUntilSeq, err = seqOfNth(pp)
	return err
}

// keepPrevious fills each zone field the publisher could not compute from the last published document (best-effort:
// without a readable previous document the fields stay unset). HeadSeq is read fresh whenever the queue is readable,
// so it is taken from the previous document only when its own read failed.
func (p *StatusPublisher) keepPrevious(ctx context.Context, eventID string, doc *dto.StatusDocument) {
	raw, ok, err := p.store.GetStatusDoc(ctx, eventID)
	if err != nil || !ok {
		return
	}
	var prev dto.StatusDocument
	if json.Unmarshal(raw, &prev) != nil {
		return
	}
	if doc.HeadSeq == nil {
		doc.HeadSeq = prev.HeadSeq
	}
	if doc.GuaranteedUntilSeq == nil {
		doc.GuaranteedUntilSeq = prev.GuaranteedUntilSeq
	}
	if doc.PossibleUntilSeq == nil {
		doc.PossibleUntilSeq = prev.PossibleUntilSeq
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
