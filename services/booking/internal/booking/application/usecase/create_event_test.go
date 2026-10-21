package usecase_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/booking/internal/booking/application/usecase"
	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/booking/internal/booking/domain/dto"
)

func TestCreateEvent_Success(t *testing.T) {
	repo := &fakeEventWriteRepo{}
	uc := usecase.NewCreateEvent(repo, &fakeIDs{id: "ev-1"})
	id, err := uc.Create(context.Background(), dto.CreateEventRequest{
		Name: "Concert", Slug: "concert", Venue: "JCC",
		StartsAt:           time.Now().Add(24 * time.Hour),
		SalesStartsAt:      time.Now(),
		SalesEndsAt:        time.Now().Add(48 * time.Hour),
		MaxTicketsPerOrder: 3, MaxTicketsPerBuyerNIK: 3,
		Categories: []dto.CreateCategoryRequest{
			{Code: "VIP", Name: "VIP", PriceMinor: 1000, Capacity: 100},
		},
	})
	require.NoError(t, err)
	assert.NotEmpty(t, id)
}

func TestCreateEvent_NoCategories(t *testing.T) {
	uc := usecase.NewCreateEvent(&fakeEventWriteRepo{}, &fakeIDs{})
	_, err := uc.Create(context.Background(), dto.CreateEventRequest{Name: "X"})
	assert.Error(t, err)
}

func TestCreateEvent_RepoError(t *testing.T) {
	repo := &fakeEventWriteRepo{createErr: errors.New("db error")}
	uc := usecase.NewCreateEvent(repo, &fakeIDs{id: "ev-1"})
	_, err := uc.Create(context.Background(), dto.CreateEventRequest{
		Name: "X", Categories: []dto.CreateCategoryRequest{{Code: "A", Capacity: 10}},
	})
	assert.Error(t, err)
}
