//go:build integration

package postgres

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/booking/internal/booking/domain/dto"
)

// seedFacetEvents gives the seeded "Konser A" a city and adds a second visible event, a CANCELLED one,
// and an unpublished one. Visible set after this:
//   - Konser A: Jakarta, genre {POP, ROCK}
//   - Konser B: Bandung, genre {POP, JAZZ}
//
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
// (published, non-cancelled); DRAFT/CANCELLED must not contribute. One round-trip returns both
// dimensions plus the full-filter count.
func TestEventRepo_Facets_VisibilityAndDistinct(t *testing.T) {
	f := seed(t)
	seedFacetEvents(t, f)
	ctx := context.Background()
	repo := NewEventRepo(f.pool)

	got, n, err := repo.Facets(ctx, dto.EventListQuery{})
	require.NoError(t, err)
	assert.Equal(t, []string{"Bandung", "Jakarta"}, got.Cities,
		"distinct + terurut; Surabaya (cancelled) dan Medan (draft) dikecualikan")
	assert.Equal(t, []string{"JAZZ", "POP", "ROCK"}, got.Genres,
		"distinct lintas event + terurut; METAL/FOLK dikecualikan; POP tidak diduplikasi")
	assert.Equal(t, 2, n, "dua event terlihat (Konser A + Konser B)")
}

// TC-CAT-11 drill-down (now intrinsic to Facets, in one query): filtering by city narrows the GENRES
// facet to that city, while the CITIES facet ignores its own city filter so the city picker stays
// complete. Symmetrically for a genres filter.
func TestEventRepo_Facets_DrillDown(t *testing.T) {
	f := seed(t)
	seedFacetEvents(t, f)
	ctx := context.Background()
	repo := NewEventRepo(f.pool)

	// city=Bandung: genres facet follows the city -> only Bandung's {JAZZ, POP} (ROCK is Jakarta-only);
	// cities facet ignores its own city filter -> full city list.
	got, n, err := repo.Facets(ctx, dto.EventListQuery{City: "Bandung"})
	require.NoError(t, err)
	assert.Equal(t, []string{"JAZZ", "POP"}, got.Genres, "genre facet mengikuti filter city")
	assert.Equal(t, []string{"Bandung", "Jakarta"}, got.Cities, "cities facet mengabaikan filter city-nya sendiri")
	assert.Equal(t, 1, n, "hanya Konser B di Bandung")

	// genres=POP: in both cities -> cities facet lists both; genres facet ignores its own genres filter.
	got, _, err = repo.Facets(ctx, dto.EventListQuery{Genres: []string{"POP"}})
	require.NoError(t, err)
	assert.Equal(t, []string{"Bandung", "Jakarta"}, got.Cities, "POP ada di kedua kota")

	// genres=ROCK: only in Jakarta -> cities facet narrows to Jakarta.
	got, _, err = repo.Facets(ctx, dto.EventListQuery{Genres: []string{"ROCK"}})
	require.NoError(t, err)
	assert.Equal(t, []string{"Jakarta"}, got.Cities, "ROCK hanya di Jakarta")
}

// Empty visible catalog yields empty slices (never nil) and count 0, not an error.
func TestEventRepo_Facets_EmptyWhenNoVisibleEvents(t *testing.T) {
	f := seed(t)
	ctx := context.Background()
	repo := NewEventRepo(f.pool)
	_, err := f.pool.Exec(ctx, `UPDATE booking.events SET published_at = NULL WHERE id=$1::uuid`, f.eventID)
	require.NoError(t, err)

	got, n, err := repo.Facets(ctx, dto.EventListQuery{})
	require.NoError(t, err)
	assert.Empty(t, got.Cities)
	assert.Empty(t, got.Genres)
	assert.Equal(t, 0, n)
}

// The count returned by Facets uses the same filter predicate as ListEvents (shared eventFilterWhere), so
// a combined filter with no matches returns 0 even though each dimension's drill-down facet is non-empty.
// This is exactly the signal the usecase uses for the option-c empty guard.
func TestEventRepo_Facets_CountSharedPredicateVsDrillDown(t *testing.T) {
	f := seed(t)
	seedFacetEvents(t, f)
	ctx := context.Background()
	repo := NewEventRepo(f.pool)

	// Bandung has POP,JAZZ; no Bandung event has ROCK -> full filter matches nothing (count 0)...
	got, n, err := repo.Facets(ctx, dto.EventListQuery{City: "Bandung", Genres: []string{"ROCK"}})
	require.NoError(t, err)
	assert.Equal(t, 0, n, "city+genre yang bertentangan -> 0 event (dasar guard facet kosong)")
	// ...yet the drill-down facets each stay non-empty (cities drops city, genres drops genres), which is
	// precisely why the count is needed as a separate guard signal.
	assert.NotEmpty(t, got.Cities, "cities drill-down (drop city) tetap ada isinya")
	assert.NotEmpty(t, got.Genres, "genres drill-down (drop genres) tetap ada isinya")
}
