package usecase_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/booking/internal/booking/application/usecase"
	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/booking/internal/booking/domain/entity"
	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/booking/internal/booking/port/inbound"
)

func TestGetEventSupply_Success(t *testing.T) {
	detail := entity.EventDetail{
		Event: entity.Event{ID: eventID, Published: true, Status: entity.EventAvailable},
		Categories: []entity.CategoryDetail{
			{Category: entity.Category{Code: "VIP", Name: "VIP"}, Capacity: 7, Available: 7},
			{Category: entity.Category{Code: "REG", Name: "Regular"}, Capacity: 0, Available: 0},
		},
	}
	repo := &fakeEventRepo{detail: detail, supply: entity.Supply{EventID: eventID, TotalCapacity: 7}}
	uc := usecase.NewGetEventSupply(repo)

	snap, err := uc.GetSupply(context.Background(), eventID)
	require.NoError(t, err)
	assert.Equal(t, eventID, snap.EventID)
	assert.Equal(t, 7, snap.RemainingTickets)
	assert.False(t, snap.SoldOut)
	require.Len(t, snap.Categories, 2)

	vip := snap.Categories[0]
	assert.Equal(t, "VIP", vip.Code)
	assert.Equal(t, 7, vip.Total)
	assert.Equal(t, 0, vip.Sold)

	reg := snap.Categories[1]
	assert.Equal(t, "REG", reg.Code)
	assert.Equal(t, 0, reg.Total)
}

func TestGetEventSupply_EventNotFound(t *testing.T) {
	uc := usecase.NewGetEventSupply(&fakeEventRepo{err: inbound.ErrNotFound})
	_, err := uc.GetSupply(context.Background(), eventID)
	assert.ErrorIs(t, err, inbound.ErrNotFound)
}

func TestGetEventSupply_RepoError(t *testing.T) {
	repoErr := errors.New("db down")
	uc := usecase.NewGetEventSupply(&fakeEventRepo{err: repoErr})
	_, err := uc.GetSupply(context.Background(), eventID)
	assert.ErrorIs(t, err, repoErr)
}

// RemainingTickets is the event's free units (total - allocated - held - sold), not the sum of category
// availability, and the categories carry the real sold and capacity from the detail.
func TestGetEventSupply_RemainingIsFreeUnits(t *testing.T) {
	detail := entity.EventDetail{
		Event: entity.Event{ID: eventID, Published: true, Status: entity.EventAvailable},
		Categories: []entity.CategoryDetail{
			{Category: entity.Category{Code: "VIP"}, Capacity: 10, Sold: 1, Available: 3},
		},
	}
	repo := &fakeEventRepo{detail: detail, supply: entity.Supply{
		EventID: eventID, TotalCapacity: 10, AllocatedUnits: 4, HeldUnits: 2, SoldUnits: 1,
	}}
	snap, err := usecase.NewGetEventSupply(repo).GetSupply(context.Background(), eventID)
	require.NoError(t, err)
	assert.Equal(t, 3, snap.RemainingTickets)
	assert.Equal(t, 3, snap.FreeUnits)
	assert.False(t, snap.SoldOut)
	require.Len(t, snap.Categories, 1)
	assert.Equal(t, 1, snap.Categories[0].Sold)
	assert.Equal(t, 10, snap.Categories[0].Total)
}

// SoldOut is true only when nothing can return to sale: sold >= total, or the event is cancelled.
func TestGetEventSupply_SoldOutOnlyWhenNothingCanReturn(t *testing.T) {
	tests := map[string]struct {
		status        entity.EventStatus
		supply        entity.Supply
		wantSoldOut   bool
		wantRemaining int
	}{
		"free 0 with allocated units still returnable": {
			status:        entity.EventAvailable,
			supply:        entity.Supply{TotalCapacity: 2, AllocatedUnits: 2},
			wantSoldOut:   false,
			wantRemaining: 0,
		},
		"sold equals total": {
			status:        entity.EventAvailable,
			supply:        entity.Supply{TotalCapacity: 10, SoldUnits: 10},
			wantSoldOut:   true,
			wantRemaining: 0,
		},
		"cancelled hides free units": {
			status:        entity.EventCancelled,
			supply:        entity.Supply{TotalCapacity: 10},
			wantSoldOut:   true,
			wantRemaining: 0,
		},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			detail := entity.EventDetail{
				Event:      entity.Event{ID: eventID, Published: true, Status: tt.status},
				Categories: []entity.CategoryDetail{{Category: entity.Category{Code: "VIP"}, Capacity: 10}},
			}
			repo := &fakeEventRepo{detail: detail, supply: tt.supply}
			snap, err := usecase.NewGetEventSupply(repo).GetSupply(context.Background(), eventID)
			require.NoError(t, err)
			assert.Equal(t, tt.wantSoldOut, snap.SoldOut)
			assert.Equal(t, tt.wantRemaining, snap.RemainingTickets)
		})
	}
}

// A stored SOLD_OUT status is no longer read: with stock left the snapshot is not sold out.
func TestGetEventSupply_StoredSoldOutIsIgnored(t *testing.T) {
	detail := entity.EventDetail{
		Event:      entity.Event{ID: eventID, Published: true, Status: entity.EventSoldOut},
		Categories: []entity.CategoryDetail{{Category: entity.Category{Code: "VIP"}, Capacity: 10, Available: 10}},
	}
	repo := &fakeEventRepo{detail: detail, supply: entity.Supply{EventID: eventID, TotalCapacity: 10}}
	snap, err := usecase.NewGetEventSupply(repo).GetSupply(context.Background(), eventID)
	require.NoError(t, err)
	assert.False(t, snap.SoldOut, "stored SOLD_OUT must not decide soldOut while stock is available")
	assert.Equal(t, 10, snap.RemainingTickets)
}

func TestGetEventSupply_SoldOut(t *testing.T) {
	detail := entity.EventDetail{
		Event: entity.Event{ID: eventID, Published: true, Status: entity.EventAvailable},
		Categories: []entity.CategoryDetail{
			{Category: entity.Category{Code: "VIP"}, Capacity: 10, Sold: 10},
		},
	}
	repo := &fakeEventRepo{detail: detail, supply: entity.Supply{EventID: eventID, TotalCapacity: 10, SoldUnits: 10}}
	snap, err := usecase.NewGetEventSupply(repo).GetSupply(context.Background(), eventID)
	require.NoError(t, err)
	assert.True(t, snap.SoldOut)
	assert.Equal(t, 0, snap.RemainingTickets)
}

func TestGetEventSupply_MultipleCategories(t *testing.T) {
	detail := entity.EventDetail{
		Event: entity.Event{ID: eventID, Published: true, Status: entity.EventAvailable},
		Categories: []entity.CategoryDetail{
			{Category: entity.Category{Code: "A"}, Capacity: 10, Available: 10},
			{Category: entity.Category{Code: "B"}, Capacity: 5, Available: 5},
			{Category: entity.Category{Code: "C"}, Capacity: 0, Available: 0},
		},
	}
	repo := &fakeEventRepo{detail: detail, supply: entity.Supply{EventID: eventID, TotalCapacity: 15}}
	snap, err := usecase.NewGetEventSupply(repo).GetSupply(context.Background(), eventID)
	require.NoError(t, err)
	assert.Equal(t, 15, snap.RemainingTickets)
	require.Len(t, snap.Categories, 3)
}

func TestGetEventSupply_LedgerFieldsAndWindow(t *testing.T) {
	start := time.Date(2030, 1, 1, 10, 0, 0, 0, time.UTC)
	detail := entity.EventDetail{
		Event: entity.Event{ID: eventID, Published: true, Status: entity.EventAvailable, MaxTicketsPerOrder: 3,
			SalesStartsAt: start, SalesEndsAt: start.Add(4 * time.Hour)},
		Categories: []entity.CategoryDetail{{Category: entity.Category{Code: "A"}, Capacity: 12, Available: 2}},
	}
	repo := &fakeEventRepo{detail: detail, supply: entity.Supply{TotalCapacity: 12, AllocatedUnits: 6, HeldUnits: 4}}
	snap, err := usecase.NewGetEventSupply(repo).GetSupply(context.Background(), eventID)
	require.NoError(t, err)
	assert.Equal(t, 6, snap.AllocatedUnits)
	assert.Equal(t, 4, snap.HeldUnits)
	assert.Equal(t, 2, snap.FreeUnits)
	assert.Equal(t, 2, snap.RemainingTickets)
	assert.Equal(t, 3, snap.MaxTicketsPerOrder)
	assert.Equal(t, start, snap.SalesStartsAt)
}
