//go:build integration

package postgres

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/booking/internal/booking/application/usecase"
	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/booking/internal/booking/domain/dto"
	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/booking/internal/booking/infrastructure/ids"
	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/booking/internal/booking/port/inbound"
)

// adminCreate drives the real admin usecase + repository (no mocks) and returns the new event id.
func adminCreate(t *testing.T, f fixture, slug, city string, genres []string) string {
	t.Helper()
	now := time.Now()
	id, err := usecase.NewCreateEvent(NewEventWriteRepo(f.pool), ids.New()).Create(context.Background(), dto.CreateEventRequest{
		Name: slug, Slug: slug, Venue: "GBK",
		StartsAt: now.Add(72 * time.Hour), SalesStartsAt: now.Add(-time.Hour), SalesEndsAt: now.Add(48 * time.Hour),
		MaxTicketsPerOrder: 3, MaxTicketsPerBuyerNIK: 3, City: city, Genres: genres, Categories: priced(100000),
	})
	require.NoError(t, err)
	return id
}

func adminPatch(f fixture, id string, req dto.UpdateEventConfigRequest) error {
	return usecase.NewUpdateEventConfig(NewEventWriteRepo(f.pool)).Update(context.Background(), id, req)
}

func (f fixture) cityGenre(t *testing.T, id string) (city string, genre []string) {
	t.Helper()
	require.NoError(t, f.pool.QueryRow(context.Background(),
		`SELECT city, genre FROM booking.events WHERE id=$1::uuid`, id).Scan(&city, &genre))
	return city, genre
}

func listedIDs(t *testing.T, repo *EventRepo, q dto.EventListQuery) map[string]bool {
	t.Helper()
	evs, err := repo.ListEvents(context.Background(), q)
	require.NoError(t, err)
	out := map[string]bool{}
	for _, e := range evs {
		out[e.ID] = true
	}
	return out
}

// TC-CAT-12: an admin-created event with city + genres is stored canonicalised and is found by the public
// city/genres filters and facets once published; an event created without them stores '' / '{}' (never NULL).
func TestCityGenres_CreateThenFilterAndFacets(t *testing.T) {
	f := seed(t)
	ctx := context.Background()
	repo := NewEventRepo(f.pool)
	w := NewEventWriteRepo(f.pool)

	id := adminCreate(t, f, "cg-create", " Jakarta ", []string{"pop", "indie-rock", " "})
	bare := adminCreate(t, f, "cg-bare", "", nil)
	require.NoError(t, w.SetPublished(ctx, id, true))
	require.NoError(t, w.SetPublished(ctx, bare, true))

	city, genre := f.cityGenre(t, id)
	assert.Equal(t, "Jakarta", city)
	assert.Equal(t, []string{"INDIE_ROCK", "POP"}, genre)
	city, genre = f.cityGenre(t, bare)
	assert.Equal(t, "", city)
	assert.NotNil(t, genre, "genre must be '{}' not NULL")
	assert.Empty(t, genre)
	assert.Equal(t, 1, f.n(t, `SELECT count(*) FROM booking.events WHERE id=$1::uuid AND genre = '{}'`, bare))

	for name, q := range map[string]dto.EventListQuery{
		"city lower":      {City: "jakarta"},
		"city upper":      {City: "JAKARTA"},
		"genre POP":       {Genres: []string{"POP"}},
		"genre INDIE":     {Genres: []string{"INDIE_ROCK"}},
		"genre overlap":   {Genres: []string{"JAZZ", "POP"}},
		"city and genres": {City: "jakarta", Genres: []string{"POP"}},
	} {
		assert.True(t, listedIDs(t, repo, q)[id], name)
	}
	assert.False(t, listedIDs(t, repo, dto.EventListQuery{City: "jakarta"})[bare], "event without city is not matched")
	assert.False(t, listedIDs(t, repo, dto.EventListQuery{Genres: []string{"POP"}})[bare])

	facets, _, err := repo.Facets(ctx, dto.EventListQuery{})
	require.NoError(t, err)
	assert.Contains(t, facets.Cities, "Jakarta")
	assert.Contains(t, facets.Genres, "INDIE_ROCK")
	assert.Contains(t, facets.Genres, "POP")
}

// Facet case-folding observation (design §9, recorded for the evidence doc): Facets uses SELECT DISTINCT e.city,
// so "Jakarta" and "jakarta" are TWO facet entries even though the city FILTER is case-insensitive.
// Known limitation, out of scope here.
func TestCityGenres_FacetsDoNotFoldCityCase(t *testing.T) {
	f := seed(t)
	ctx := context.Background()
	w := NewEventWriteRepo(f.pool)
	a := adminCreate(t, f, "cg-case-a", "Jakarta", nil)
	b := adminCreate(t, f, "cg-case-b", "jakarta", nil)
	require.NoError(t, w.SetPublished(ctx, a, true))
	require.NoError(t, w.SetPublished(ctx, b, true))

	facets, _, err := NewEventRepo(f.pool).Facets(ctx, dto.EventListQuery{})
	require.NoError(t, err)
	assert.Contains(t, facets.Cities, "Jakarta")
	assert.Contains(t, facets.Cities, "jakarta")
	got := listedIDs(t, NewEventRepo(f.pool), dto.EventListQuery{City: "JAKARTA"})
	assert.True(t, got[a] && got[b], "the filter itself is case-insensitive")
}

// TC-CAT-13: PATCH set / replace / clear / omit, city-only PATCH persists, unknown event stays not-found.
func TestCityGenres_Patch(t *testing.T) {
	f := seed(t)
	strp := func(s string) *string { return &s }
	slp := func(s ...string) *[]string { out := append([]string{}, s...); return &out }
	id := adminCreate(t, f, "cg-patch", "Jakarta", []string{"pop"})

	// omit both (TTL only): unchanged
	ttl := 300
	require.NoError(t, adminPatch(f, id, dto.UpdateEventConfigRequest{SelectionTTLSeconds: &ttl}))
	city, genre := f.cityGenre(t, id)
	assert.Equal(t, "Jakarta", city)
	assert.Equal(t, []string{"POP"}, genre)

	// city-only PATCH persists (not the no-op path) and leaves genres alone
	require.NoError(t, adminPatch(f, id, dto.UpdateEventConfigRequest{City: strp("  Bandung ")}))
	city, genre = f.cityGenre(t, id)
	assert.Equal(t, "Bandung", city)
	assert.Equal(t, []string{"POP"}, genre)

	// replace genres, canonicalised; city untouched
	require.NoError(t, adminPatch(f, id, dto.UpdateEventConfigRequest{Genres: slp("rock", "heavy metal")}))
	city, genre = f.cityGenre(t, id)
	assert.Equal(t, "Bandung", city)
	assert.Equal(t, []string{"HEAVY_METAL", "ROCK"}, genre)

	// clear both
	require.NoError(t, adminPatch(f, id, dto.UpdateEventConfigRequest{City: strp(""), Genres: slp()}))
	city, genre = f.cityGenre(t, id)
	assert.Equal(t, "", city)
	assert.Empty(t, genre)
	assert.Equal(t, 1, f.n(t, `SELECT count(*) FROM booking.events WHERE id=$1::uuid AND genre = '{}'`, id), "cleared genres are '{}' not NULL")

	// patched values are visible to the filters immediately (no server cache)
	require.NoError(t, NewEventWriteRepo(f.pool).SetPublished(context.Background(), id, true))
	require.NoError(t, adminPatch(f, id, dto.UpdateEventConfigRequest{City: strp("Medan"), Genres: slp("folk")}))
	assert.True(t, listedIDs(t, NewEventRepo(f.pool), dto.EventListQuery{City: "medan", Genres: []string{"FOLK"}})[id])

	// unknown event keeps the existing not-found behaviour
	err := adminPatch(f, "00000000-0000-0000-0000-000000000000", dto.UpdateEventConfigRequest{City: strp("X")})
	assert.ErrorIs(t, err, inbound.ErrNotFound)
}

// TC-CAT-14 (atomic rejection): invalid input writes nothing — no event, supply or category rows.
func TestCityGenres_RejectedInputWritesNothing(t *testing.T) {
	f := seed(t)
	before := [3]int{
		f.n(t, `SELECT count(*) FROM booking.events`),
		f.n(t, `SELECT count(*) FROM booking.event_supply`),
		f.n(t, `SELECT count(*) FROM booking.ticket_categories`),
	}
	tooMany := make([]string, 21)
	for i := range tooMany {
		tooMany[i] = "g" + string(rune('a'+i))
	}
	now := time.Now()
	_, err := usecase.NewCreateEvent(NewEventWriteRepo(f.pool), ids.New()).Create(context.Background(), dto.CreateEventRequest{
		Name: "x", Slug: "cg-reject", Venue: "GBK", StartsAt: now, SalesStartsAt: now, SalesEndsAt: now,
		MaxTicketsPerOrder: 3, MaxTicketsPerBuyerNIK: 3, Genres: tooMany, Categories: priced(1000),
	})
	require.Error(t, err)
	after := [3]int{
		f.n(t, `SELECT count(*) FROM booking.events`),
		f.n(t, `SELECT count(*) FROM booking.event_supply`),
		f.n(t, `SELECT count(*) FROM booking.ticket_categories`),
	}
	assert.Equal(t, before, after)
}
