package usecase

import (
	"context"

	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/booking/internal/booking/domain/dto"
	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/booking/internal/booking/domain/entity"
	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/booking/internal/booking/domain/mapper"
	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/booking/internal/booking/port/outbound"
)

// ListFacets serves the catalog filter values (cities, genres) as a drill-down: each facet reflects the
// current event-list query but ignores its own filter, so the values always match what the list can show
// without hiding the other choices on the dimension being selected. When the combined filter yields no
// events, both facets are empty so they never contradict a zero-result list (option c).
type ListFacets struct {
	events outbound.IEventRepo
}

// NewListFacets wires the facets usecase.
func NewListFacets(events outbound.IEventRepo) *ListFacets {
	return &ListFacets{events: events}
}

// GetFacets returns the distinct cities and genres available under the query. The query is normalised with
// the same rules as the event list (enum fail-soft; bad dates / genre caps -> VALIDATION_ERROR), so the two
// endpoints behave identically on the same params. Drill-down: the cities facet runs with the city filter
// cleared, the genres facet with the genres filter cleared.
func (uc *ListFacets) GetFacets(ctx context.Context, q dto.EventListQuery) (dto.EventFacets, error) {
	norm, err := normalizeEventQuery(q)
	if err != nil {
		return dto.EventFacets{}, err
	}

	// Alignment guard (option c): if the full combined filter yields no events, the facets must mirror the
	// empty list — never offer options that contradict a zero-result catalog. Drill-down hints only make
	// sense when the current query actually returns something.
	n, err := uc.events.CountEvents(ctx, norm)
	if err != nil {
		return dto.EventFacets{}, err
	}
	if n == 0 {
		return mapper.ToEventFacets(entity.EventFacets{}), nil // {cities:[], genres:[]}
	}

	cityQuery := norm
	cityQuery.City = "" // drill-down: cities ignore the city filter
	cities, err := uc.events.ListFacets(ctx, cityQuery, outbound.FacetCities)
	if err != nil {
		return dto.EventFacets{}, err
	}

	genreQuery := norm
	genreQuery.Genres = nil // drill-down: genres ignore the genres filter
	genres, err := uc.events.ListFacets(ctx, genreQuery, outbound.FacetGenres)
	if err != nil {
		return dto.EventFacets{}, err
	}

	return mapper.ToEventFacets(entity.EventFacets{Cities: cities, Genres: genres}), nil
}
