//go:build integration

package postgres

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/booking/internal/booking/domain/dto"
)

// createPublishedEvent creates a published event through the real admin write path with the given category prices
// (one unit of capacity each is enough; the supply trigger only needs capacities to add up).
func createPublishedEvent(t *testing.T, f fixture, slug string, req dto.CreateEventRequest) string {
	t.Helper()
	ctx := context.Background()
	id := f.q(t, `SELECT gen_random_uuid()::text`)
	now := time.Now()
	req.Slug, req.Name, req.Venue = slug, slug, "GBK"
	req.StartsAt, req.SalesStartsAt, req.SalesEndsAt = now.Add(72*time.Hour), now.Add(-time.Hour), now.Add(48*time.Hour)
	req.MaxTicketsPerOrder, req.MaxTicketsPerBuyerNIK = 3, 3
	w := NewEventWriteRepo(f.pool)
	require.NoError(t, w.CreateEvent(ctx, id, req))
	require.NoError(t, w.SetPublished(ctx, id, true))
	return id
}

func priced(prices ...int64) []dto.CreateCategoryRequest {
	out := make([]dto.CreateCategoryRequest, len(prices))
	for i, p := range prices {
		out[i] = dto.CreateCategoryRequest{Code: string(rune('A' + i)), Name: "Cat", PriceMinor: p, Capacity: 10}
	}
	return out
}

// TC-EVT-06: GetDetail.Event.MinPriceMinor is the cheapest category price and equals the list value.
func TestEventRepo_GetDetail_MinPrice(t *testing.T) {
	f := seed(t)
	ctx := context.Background()
	repo := NewEventRepo(f.pool)

	multi := createPublishedEvent(t, f, "min-multi", dto.CreateEventRequest{Categories: priced(150000, 75000, 300000)})
	single := createPublishedEvent(t, f, "min-single", dto.CreateEventRequest{Categories: priced(42000)})
	soldOut := createPublishedEvent(t, f, "min-soldout", dto.CreateEventRequest{Categories: priced(150000, 50000)})
	_, err := f.pool.Exec(ctx, `UPDATE booking.category_inventories ci SET sold_quantity = capacity
		FROM booking.ticket_categories c WHERE c.id = ci.category_id AND c.event_id = $1::uuid AND c.price_minor = 50000`, soldOut)
	require.NoError(t, err)

	list, err := repo.ListEvents(ctx, dto.EventListQuery{})
	require.NoError(t, err)
	listed := map[string]int64{}
	for _, e := range list {
		listed[e.ID] = e.MinPriceMinor
	}

	for _, tc := range []struct {
		name string
		id   string
		want int64
	}{
		{"three categories", multi, 75000},
		{"one category", single, 42000},
		{"cheaper sold-out category still counts", soldOut, 50000},
	} {
		t.Run(tc.name, func(t *testing.T) {
			d, err := repo.GetDetail(ctx, tc.id)
			require.NoError(t, err)
			assert.Equal(t, tc.want, d.Event.MinPriceMinor)
			require.Contains(t, listed, tc.id)
			assert.Equal(t, listed[tc.id], d.Event.MinPriceMinor, "detail must equal the list value")
		})
	}

	t.Run("no categories gives 0 without error", func(t *testing.T) {
		var id string
		require.NoError(t, f.pool.QueryRow(ctx, `
			INSERT INTO booking.events (slug, name, venue, starts_at, sales_starts_at, sales_ends_at, status)
			VALUES ('min-none', 'None', 'GBK', now() + interval '3 days', now(), now() + interval '2 days', 'DRAFT') RETURNING id::text`).Scan(&id))
		d, err := repo.GetDetail(ctx, id)
		require.NoError(t, err)
		assert.Equal(t, int64(0), d.Event.MinPriceMinor)
		assert.Empty(t, d.Categories)
	})
}
