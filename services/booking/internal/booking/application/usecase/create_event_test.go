package usecase_test

import (
	"context"
	"errors"
	"sort"
	"strings"
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

func cityGenresReq(city string, genres []string) dto.CreateEventRequest {
	return dto.CreateEventRequest{
		Name: "Concert", Slug: "concert", Venue: "JCC", City: city, Genres: genres,
		Categories: []dto.CreateCategoryRequest{{Code: "VIP", Name: "VIP", PriceMinor: 1000, Capacity: 100}},
	}
}

func TestCreateEvent_CityGenres(t *testing.T) {
	many := func(n int) []string {
		out := make([]string, n)
		for i := range out {
			out[i] = "g" + strings.Repeat("x", i)
		}
		return out
	}
	tests := []struct {
		name       string
		city       string
		genres     []string
		wantCity   string
		wantGenres []string
		wantField  string // non-empty: validation error expected, repo untouched
	}{
		{name: "canonicalised", city: " Jakarta ", genres: []string{"pop", "indie-rock", " "}, wantCity: "Jakarta", wantGenres: []string{"INDIE_ROCK", "POP"}},
		{name: "omitted", wantCity: "", wantGenres: nil},
		{name: "20 genres accepted", genres: many(20), wantGenres: many20Upper()},
		{name: "64 byte token accepted", genres: []string{strings.Repeat("a", 64)}, wantGenres: []string{strings.Repeat("A", 64)}},
		{name: "100 rune city accepted", city: strings.Repeat("k", 100), wantCity: strings.Repeat("k", 100)},
		{name: "21 genres rejected", genres: many(21), wantField: "genres"},
		{name: "65 byte token rejected", genres: []string{strings.Repeat("a", 65)}, wantField: "genres"},
		{name: "101 rune city rejected", city: strings.Repeat("k", 101), wantField: "city"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			repo := &fakeEventWriteRepo{}
			ids := &fakeIDs{id: "ev-1"}
			uc := usecase.NewCreateEvent(repo, ids)
			id, err := uc.Create(context.Background(), cityGenresReq(tc.city, tc.genres))
			if tc.wantField != "" {
				assertValidationField(t, err, tc.wantField)
				assert.Equal(t, 0, repo.createCalls, "rejected input must not reach the repo")
				assert.Equal(t, 0, ids.n, "validation must run before an ID is generated")
				assert.Empty(t, id)
				return
			}
			require.NoError(t, err)
			require.Equal(t, 1, repo.createCalls)
			assert.Equal(t, tc.wantCity, repo.createdReq.City)
			if tc.wantGenres == nil {
				assert.Empty(t, repo.createdReq.Genres)
			} else {
				assert.Equal(t, tc.wantGenres, repo.createdReq.Genres)
			}
		})
	}
}

func many20Upper() []string {
	out := make([]string, 20)
	for i := range out {
		out[i] = strings.ToUpper("g" + strings.Repeat("x", i))
	}
	sort.Strings(out)
	return out
}
