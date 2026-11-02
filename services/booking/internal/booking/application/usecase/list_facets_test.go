package usecase_test

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tiket-sampai-nonton/rusuh-booking-be/pkg/apperror"

	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/booking/internal/booking/application/usecase"
	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/booking/internal/booking/domain/dto"
	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/booking/internal/booking/domain/entity"
)

func TestGetFacets_PassThroughAndAlwaysArray(t *testing.T) {
	tests := []struct {
		name       string
		repo       entity.EventFacets
		wantCities []string
		wantGenres []string
	}{
		{
			name:       "pass through distinct values",
			repo:       entity.EventFacets{Cities: []string{"Bandung", "Jakarta"}, Genres: []string{"JAZZ", "POP"}},
			wantCities: []string{"Bandung", "Jakarta"},
			wantGenres: []string{"JAZZ", "POP"},
		},
		{
			name:       "nil repo slices become empty arrays",
			repo:       entity.EventFacets{},
			wantCities: []string{},
			wantGenres: []string{},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			uc := usecase.NewListFacets(&fakeEventRepo{facets: tc.repo, count: 1})
			out, err := uc.GetFacets(context.Background(), dto.EventListQuery{})
			require.NoError(t, err)
			assert.Equal(t, tc.wantCities, out.Cities)
			assert.Equal(t, tc.wantGenres, out.Genres)
			assert.NotNil(t, out.Cities)
			assert.NotNil(t, out.Genres)
		})
	}
}

// Option c: when the combined filter matches no events, both facets are empty (always-array), so they
// never contradict a zero-result list. ListFacets must not even be consulted for values.
func TestGetFacets_EmptyWhenCombinedFilterMatchesNothing(t *testing.T) {
	repo := &fakeEventRepo{
		count:  0, // combined filter -> no events
		facets: entity.EventFacets{Cities: []string{"Jakarta"}, Genres: []string{"POP"}}, // would be returned if consulted
	}
	uc := usecase.NewListFacets(repo)
	out, err := uc.GetFacets(context.Background(), dto.EventListQuery{City: "Bandung", Genres: []string{"ROCK"}})
	require.NoError(t, err)
	assert.Equal(t, []string{}, out.Cities, "list kosong -> cities facet kosong")
	assert.Equal(t, []string{}, out.Genres, "list kosong -> genres facet kosong")
	assert.NotNil(t, out.Cities)
	assert.NotNil(t, out.Genres)
}

// Drill-down: the cities facet must be queried with the city filter cleared, the genres facet with the
// genres filter cleared, while every OTHER filter is preserved (and normalised).
func TestGetFacets_DrillDownClearsOwnFilterKeepsOthers(t *testing.T) {
	repo := &fakeEventRepo{count: 1}
	uc := usecase.NewListFacets(repo)

	_, err := uc.GetFacets(context.Background(), dto.EventListQuery{
		City:   "Jakarta",
		Genres: []string{"pop"},
		Status: "AVAILABLE",
	})
	require.NoError(t, err)

	// cities facet: city cleared, but genres+status kept (and normalised: pop -> POP).
	assert.Empty(t, repo.gotCity.City, "cities facet mengabaikan filter city")
	assert.Equal(t, []string{"POP"}, repo.gotCity.Genres, "genres tetap diterapkan + ternormalisasi")
	assert.Equal(t, "AVAILABLE", repo.gotCity.Status, "status tetap diterapkan")

	// genres facet: genres cleared, but city+status kept.
	assert.Nil(t, repo.gotGenr.Genres, "genres facet mengabaikan filter genres")
	assert.Equal(t, "Jakarta", repo.gotGenr.City, "city tetap diterapkan")
	assert.Equal(t, "AVAILABLE", repo.gotGenr.Status, "status tetap diterapkan")
}

// Validation parity: the same bad input that fails the list (criterion 11) fails facets with 400.
func TestGetFacets_ValidationParity(t *testing.T) {
	uc := usecase.NewListFacets(&fakeEventRepo{})
	_, err := uc.GetFacets(context.Background(), dto.EventListQuery{StartDate: "nonsense"})
	require.Error(t, err)
	assert.Equal(t, apperror.CodeValidationFailed, codeOf(t, err))
}

// Enum stays fail-soft on facets just like the list.
func TestGetFacets_EnumFailSoft(t *testing.T) {
	repo := &fakeEventRepo{count: 1}
	uc := usecase.NewListFacets(repo)
	_, err := uc.GetFacets(context.Background(), dto.EventListQuery{Status: "bogus"})
	require.NoError(t, err)
	assert.Equal(t, "ALL", repo.gotCity.Status, "status tak dikenal -> default ALL (fail-soft)")
}

func TestGetFacets_RepoErrorPropagates(t *testing.T) {
	wantErr := errors.New("db down")
	uc := usecase.NewListFacets(&fakeEventRepo{err: wantErr})
	_, err := uc.GetFacets(context.Background(), dto.EventListQuery{})
	assert.ErrorIs(t, err, wantErr)
}
