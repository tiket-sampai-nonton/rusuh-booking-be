//go:build integration

package postgres

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/booking/internal/booking/domain/dto"
	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/booking/internal/booking/port/outbound"
)

// seedFacetEvents gives the seeded "Konser A" a city and adds a second visible event, a CANCELLED one,
// and an unpublished one. Visible set after this:
//   - Konser A: Jakarta, genre {POP, ROCK}
//   - Konser B: Bandung, genre {POP, JAZZ}
// Excluded: Konser C (CANCELLED, Surabaya/METAL), Konser D (draft, Medan/FOLK).
func seedFacetEvents(t *testing.T, f fixture) {
	t.Helper()
	ctx := context.Background()
	_, err := f.pool.Exec(ctx, `UPDATE booking.events SET city='Jakarta' WHERE id=$1::uuid`, f.eventID)
	require.NoError(t, err)
	_, err = f.pool.Exec(ctx, `
		INSERT INTO booking.events (slug, name, venue, city, starts_at, sales_starts_at, sales_ends_at, status, genre, published_at)
		VALUES ('konser-b', 'Konser B', 'GBK', 'Bandung', '2026-12-31T19:00:00Z', now() - interval '1 hour', now() + interval '1 day', 'AVAILABLE', ARRAY['POP','JAZZ'], now())`)
	require.NoError(t, err)
	_, err = f.pool.Exec(ctx, `
		INSERT INTO booking.events (slug, name, venue, city, starts_at, sales_starts_at, sales_ends_at, status, genre, published_at)
		VALUES ('konser-c', 'Konser C', 'GBK', 'Surabaya', '2026-12-31T19:00:00Z', now() - interval '1 hour', now() + interval '1 day', 'CANCELLED', ARRAY['METAL'], now())`)
	require.NoError(t, err)
	_, err = f.pool.Exec(ctx, `
		INSERT INTO booking.events (slug, name, venue, city, starts_at, sales_starts_at, sales_ends_at, status, genre, published_at)
		VALUES ('konser-d', 'Konser D', 'GBK', 'Medan', '2026-12-31T19:00:00Z', now() - interval '1 hour', now() + interval '1 day', 'AVAILABLE', ARRAY['FOLK'], NULL)`)
	require.NoError(t, err)
}

// TC-CAT-11: facets return distinct, sorted values from events visible in the public catalog
// (published, non-cancelled); DRAFT/CANCELLED must not contribute.
func TestEventRepo_ListFacets_VisibilityAndDistinct(t *testing.T) {
	f := seed(t)
	seedFacetEvents(t, f)
	ctx := context.Background()
	repo := NewEventRepo(f.pool)

	cities, err := repo.ListFacets(ctx, dto.EventListQuery{}, outbound.FacetCities)
	require.NoError(t, err)
	assert.Equal(t, []string{"Bandung", "Jakarta"}, cities,
		"distinct + terurut; Surabaya (cancelled) dan Medan (draft) dikecualikan")

	genres, err := repo.ListFacets(ctx, dto.EventListQuery{}, outbound.FacetGenres)
	require.NoError(t, err)
	assert.Equal(t, []string{"JAZZ", "POP", "ROCK"}, genres,
		"distinct lintas event + terurut; METAL/FOLK dikecualikan; POP tidak diduplikasi")
}

// TC-CAT-11 drill-down: filtering by city narrows the GENRES facet to that city, but the CITIES facet
// must ignore its own city filter (the caller clears it) so the city picker stays complete.
func TestEventRepo_ListFacets_DrillDown(t *testing.T) {
	f := seed(t)
	seedFacetEvents(t, f)
	ctx := context.Background()
	repo := NewEventRepo(f.pool)

	// Genres facet with city=Bandung applied -> only Bandung's genres {JAZZ, POP}, ROCK (Jakarta-only) gone.
	genresInBandung, err := repo.ListFacets(ctx, dto.EventListQuery{City: "Bandung"}, outbound.FacetGenres)
	require.NoError(t, err)
	assert.Equal(t, []string{"JAZZ", "POP"}, genresInBandung, "genre facet mengikuti filter city")

	// Cities facet: the caller (usecase) clears the city filter for drill-down, so passing an empty City
	// here yields the full city list even though a genres filter is applied.
	citiesForPop, err := repo.ListFacets(ctx, dto.EventListQuery{Genres: []string{"POP"}}, outbound.FacetCities)
	require.NoError(t, err)
	assert.Equal(t, []string{"Bandung", "Jakarta"}, citiesForPop,
		"POP ada di kedua kota -> dua kota; city facet mengikuti filter genres")

	// A genre only in Jakarta narrows the cities facet to Jakarta.
	citiesForRock, err := repo.ListFacets(ctx, dto.EventListQuery{Genres: []string{"ROCK"}}, outbound.FacetCities)
	require.NoError(t, err)
	assert.Equal(t, []string{"Jakarta"}, citiesForRock, "ROCK hanya di Jakarta")
}

// Empty visible catalog yields empty slices, not an error.
func TestEventRepo_ListFacets_EmptyWhenNoVisibleEvents(t *testing.T) {
	f := seed(t)
	ctx := context.Background()
	repo := NewEventRepo(f.pool)
	_, err := f.pool.Exec(ctx, `UPDATE booking.events SET published_at = NULL WHERE id=$1::uuid`, f.eventID)
	require.NoError(t, err)

	cities, err := repo.ListFacets(ctx, dto.EventListQuery{}, outbound.FacetCities)
	require.NoError(t, err)
	assert.Empty(t, cities)
	genres, err := repo.ListFacets(ctx, dto.EventListQuery{}, outbound.FacetGenres)
	require.NoError(t, err)
	assert.Empty(t, genres)
}

// CountEvents backs the option-c empty guard: it uses the same filter predicate as ListEvents, so a
// combined filter with no matches returns 0 even though each dimension alone is non-empty.
func TestEventRepo_CountEvents_SharedPredicate(t *testing.T) {
	f := seed(t)
	seedFacetEvents(t, f)
	ctx := context.Background()
	repo := NewEventRepo(f.pool)

	// Bandung has POP,JAZZ; no Bandung event has ROCK -> combined filter matches nothing.
	n, err := repo.CountEvents(ctx, dto.EventListQuery{City: "Bandung", Genres: []string{"ROCK"}})
	require.NoError(t, err)
	assert.Equal(t, 0, n, "city+genre yang bertentangan -> 0 event (dasar guard facet kosong)")

	// But each dimension alone is non-empty.
	n, err = repo.CountEvents(ctx, dto.EventListQuery{City: "Bandung"})
	require.NoError(t, err)
	assert.Positive(t, n, "Bandung sendiri punya event")
	n, err = repo.CountEvents(ctx, dto.EventListQuery{Genres: []string{"ROCK"}})
	require.NoError(t, err)
	assert.Positive(t, n, "ROCK sendiri punya event")
}
