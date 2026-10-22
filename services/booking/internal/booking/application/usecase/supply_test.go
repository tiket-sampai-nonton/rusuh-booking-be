package usecase_test

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/booking/internal/booking/application/usecase"
	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/booking/internal/booking/domain/entity"
	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/booking/internal/booking/port/inbound"
)

func TestGetEventSupply_Success(t *testing.T) {
	detail := entity.EventDetail{
		Event: entity.Event{ID: eventID, Status: entity.EventAvailable},
		Categories: []entity.CategoryDetail{
			{Category: entity.Category{Code: "VIP", Name: "VIP"}, Available: 7},
			{Category: entity.Category{Code: "REG", Name: "Regular"}, Available: 0},
		},
	}
	uc := usecase.NewGetEventSupply(&fakeEventRepo{detail: detail})

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

func TestGetEventSupply_SoldOut(t *testing.T) {
	detail := entity.EventDetail{
		Event: entity.Event{ID: eventID, Status: entity.EventSoldOut},
		Categories: []entity.CategoryDetail{
			{Category: entity.Category{Code: "VIP"}, Available: 0},
		},
	}
	uc := usecase.NewGetEventSupply(&fakeEventRepo{detail: detail})
	snap, err := uc.GetSupply(context.Background(), eventID)
	require.NoError(t, err)
	assert.True(t, snap.SoldOut)
	assert.Equal(t, 0, snap.RemainingTickets)
}

func TestGetEventSupply_MultipleCategories(t *testing.T) {
	detail := entity.EventDetail{
		Event: entity.Event{ID: eventID, Status: entity.EventAvailable},
		Categories: []entity.CategoryDetail{
			{Category: entity.Category{Code: "A"}, Available: 10},
			{Category: entity.Category{Code: "B"}, Available: 5},
			{Category: entity.Category{Code: "C"}, Available: 0},
		},
	}
	uc := usecase.NewGetEventSupply(&fakeEventRepo{detail: detail})
	snap, err := uc.GetSupply(context.Background(), eventID)
	require.NoError(t, err)
	assert.Equal(t, 15, snap.RemainingTickets)
	require.Len(t, snap.Categories, 3)
}
