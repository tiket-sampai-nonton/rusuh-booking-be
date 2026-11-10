//go:build integration

package postgres

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/booking/internal/booking/domain/dto"
	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/booking/internal/booking/port/inbound"
)

// stockSnapshot captures everything city/genres edits must never touch (invariants 1-6).
func stockSnapshot(t *testing.T, f fixture, id string) string {
	t.Helper()
	return f.q(t, `
		SELECT jsonb_build_object(
		  'supply',   (SELECT to_jsonb(s) FROM booking.event_supply s WHERE s.event_id = $1::uuid),
		  'inv',      (SELECT jsonb_agg(to_jsonb(i) ORDER BY i.category_id) FROM booking.category_inventories i WHERE i.event_id = $1::uuid),
		  'locked',   (SELECT rules_locked_at FROM booking.events WHERE id = $1::uuid),
		  'rules',    (SELECT jsonb_build_array(max_tickets_per_order, max_tickets_per_buyer_nik, allow_mixed_categories,
		                       selection_ttl_seconds, payment_ttl_seconds) FROM booking.events WHERE id = $1::uuid)
		)::text`, id)
}

// TC-CAT-15 (post-sale, D1): once sales are open and the rules are locked, city/genres can still be changed
// through the usecase, and stock, quota rules, timers and rules_locked_at are byte-identical afterwards.
func TestCityGenres_PostSaleChangeLeavesStockAndRulesUntouched(t *testing.T) {
	f := seed(t)
	ctx := context.Background()
	id := adminCreate(t, f, "cg-postsale", "Jakarta", []string{"pop"})
	_, err := f.pool.Exec(ctx, `UPDATE booking.events SET status='AVAILABLE' WHERE id=$1::uuid`, id)
	require.NoError(t, err)
	require.Equal(t, 1, f.n(t, `SELECT count(*) FROM booking.events WHERE id=$1::uuid AND rules_locked_at IS NOT NULL`, id), "rules are locked")

	before := stockSnapshot(t, f, id)
	city, genres := "Surabaya", []string{"jazz"}
	require.NoError(t, adminPatch(f, id, dto.UpdateEventConfigRequest{City: &city, Genres: &genres}))

	gotCity, gotGenre := f.cityGenre(t, id)
	assert.Equal(t, "Surabaya", gotCity)
	assert.Equal(t, []string{"JAZZ"}, gotGenre)
	assert.Equal(t, before, stockSnapshot(t, f, id))
}

// TC-CAT-15 (DB-level lock, design P6): the guard trigger still rejects a guarded column after the lock and, being
// one statement, persists nothing — not even city/genre in the same UPDATE. city/genre alone are not guarded.
// The PATCH API cannot mix guarded fields (DisallowUnknownFields), so this is verified at trigger level.
func TestCityGenres_LockedRulesRejectMixedUpdateAtomically(t *testing.T) {
	f := seed(t)
	ctx := context.Background()
	id := adminCreate(t, f, "cg-lock", "Jakarta", []string{"pop"})
	_, err := f.pool.Exec(ctx, `UPDATE booking.events SET status='AVAILABLE' WHERE id=$1::uuid`, id)
	require.NoError(t, err)

	_, err = f.pool.Exec(ctx, `UPDATE booking.events SET city='X', genre='{A}', max_tickets_per_order=1 WHERE id=$1::uuid`, id)
	require.Error(t, err)
	assert.ErrorIs(t, mapEventWriteErr(err, "test"), inbound.ErrRulesLocked)
	city, genre := f.cityGenre(t, id)
	assert.Equal(t, "Jakarta", city, "nothing persists from a rejected statement")
	assert.Equal(t, []string{"POP"}, genre)

	_, err = f.pool.Exec(ctx, `UPDATE booking.events SET city='X', genre='{A}' WHERE id=$1::uuid`, id)
	require.NoError(t, err, "city/genre are not guarded columns")
	city, genre = f.cityGenre(t, id)
	assert.Equal(t, "X", city)
	assert.Equal(t, []string{"A"}, genre)
}
