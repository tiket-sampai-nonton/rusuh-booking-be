package worker_test

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tiket-sampai-nonton/rusuh-booking-be/pkg/clock"
	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/queue/internal/queue/port/outbound"
)

func req(ids ...string) outbound.AllocationBatchRequest {
	entries := make([]outbound.AllocationEntryRequest, len(ids))
	for i, id := range ids {
		entries[i] = outbound.AllocationEntryRequest{QueueEntryID: id, Units: 1}
	}
	return outbound.AllocationBatchRequest{EventID: "ev", Entries: entries}
}

// s1: a replay inside the window returns the same id/expiresAt; it still replays after the deadline but before sweep.
func TestBookingStub_S1_ReplayKeepsOriginalExpiry(t *testing.T) {
	clk := &clock.Fixed{T: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)}
	s := newBookingStub(clk, time.Minute, 5)
	ctx := context.Background()
	resp, err := s.ReserveBatch(ctx, req("e1"))
	require.NoError(t, err)
	first := resp.Allocations[0]

	resp2, err := s.ReserveBatch(ctx, req("e1"))
	require.NoError(t, err)
	assert.Equal(t, first, resp2.Allocations[0])

	s.AdvanceClock(2 * time.Minute) // past expiry, before SweepSessions
	resp3, err := s.ReserveBatch(ctx, req("e1"))
	require.NoError(t, err)
	assert.Equal(t, first, resp3.Allocations[0], "masih replay dengan deadline asli sebelum sweeper berjalan")
}

// s2: once RELEASED or EXPIRED a row never grants again.
func TestBookingStub_S2_DeadRowIsPermanent(t *testing.T) {
	clk := &clock.Fixed{T: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)}
	s := newBookingStub(clk, time.Minute, 5)
	ctx := context.Background()
	resp, err := s.ReserveBatch(ctx, req("e1"))
	require.NoError(t, err)
	require.NoError(t, s.ReleaseAllocation(ctx, resp.Allocations[0].AllocationID, "x"))
	for i := 0; i < 3; i++ {
		r, err := s.ReserveBatch(ctx, req("e1"))
		require.NoError(t, err)
		assert.Equal(t, "ALREADY_ACTIVE", r.Allocations[0].Status)
	}
	s.AdvanceClock(time.Hour)
	s.SweepSessions()
	r, err := s.ReserveBatch(ctx, req("e1"))
	require.NoError(t, err)
	assert.Equal(t, "ALREADY_ACTIVE", r.Allocations[0].Status)
}

func TestBookingStub_S2_ExpiredRowIsAlsoPermanent(t *testing.T) {
	clk := &clock.Fixed{T: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)}
	s := newBookingStub(clk, time.Minute, 5)
	ctx := context.Background()
	_, err := s.ReserveBatch(ctx, req("e1"))
	require.NoError(t, err)
	s.AdvanceClock(2 * time.Minute)
	s.SweepSessions()
	for i := 0; i < 3; i++ {
		r, err := s.ReserveBatch(ctx, req("e1"))
		require.NoError(t, err)
		assert.Equal(t, "ALREADY_ACTIVE", r.Allocations[0].Status)
	}
}

// s3: NO_SUPPLY_NOW (no stock or failEntry) leaves no row and does not change stock.
func TestBookingStub_S3_NoSupplyLeavesNoRowAndNoStockChange(t *testing.T) {
	clk := &clock.Fixed{T: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)}
	s := newBookingStub(clk, time.Minute, 0)
	ctx := context.Background()
	r, err := s.ReserveBatch(ctx, req("e1"))
	require.NoError(t, err)
	assert.Equal(t, "NO_SUPPLY_NOW", r.Allocations[0].Status)
	assert.Equal(t, 0, s.stock)
	assert.Empty(t, s.rows)

	s2 := newBookingStub(clk, time.Minute, 5)
	s2.failEntry("e1")
	r2, err := s2.ReserveBatch(ctx, req("e1"))
	require.NoError(t, err)
	assert.Equal(t, "NO_SUPPLY_NOW", r2.Allocations[0].Status)
	assert.Equal(t, 5, s2.stock, "failEntry tidak mengubah stok walau stok tersedia")
	assert.Empty(t, s2.rows)
}

// s4: afterEntry turns [NO_SUPPLY_NOW, NO_SUPPLY_NOW] into [NO_SUPPLY_NOW, GRANTED].
func TestBookingStub_S4_AfterEntryHookReturnsStockMidBatch(t *testing.T) {
	clk := &clock.Fixed{T: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)}
	s := newBookingStub(clk, time.Minute, 0)
	s.failEntry("e1")
	s.afterEntry(0, func() { s.stock++ })
	resp, err := s.ReserveBatch(context.Background(), req("e1", "e2"))
	require.NoError(t, err)
	assert.Equal(t, "NO_SUPPLY_NOW", resp.Allocations[0].Status)
	assert.Equal(t, "GRANTED", resp.Allocations[1].Status)
}

// s5: a method-set guard so a future re-grant helper is caught.
func TestBookingStub_S5_MethodSetHasNoReGrantOperation(t *testing.T) {
	typ := reflectTypeOfStub()
	names := map[string]bool{}
	for i := 0; i < typ.NumMethod(); i++ {
		names[typ.Method(i).Name] = true
	}
	allowed := map[string]bool{
		"ReserveBatch": true, "ReleaseAllocation": true, "Supply": true,
		"SweepSessions": true, "AdvanceClock": true, "Calls": true, "ReleasedIDs": true,
		"failEntry": true, "clearFail": true, "afterEntry": true, "complete": true,
	}
	for n := range names {
		assert.True(t, allowed[n], "metode tak terduga %s — mungkin operasi re-grant", n)
	}
}
