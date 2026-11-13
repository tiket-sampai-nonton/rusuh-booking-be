package delivery

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tiket-sampai-nonton/rusuh-booking-be/pkg/clock"

	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/booking/internal/booking/application/usecase"
	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/booking/internal/booking/domain/entity"
	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/booking/internal/booking/port/outbound"
)

// publishRepo is an event source whose published flag an admin write can change.
type publishRepo struct {
	outbound.IEventRepo
	published bool
}

func (r *publishRepo) GetDetail(_ context.Context, id string) (entity.EventDetail, error) {
	return entity.EventDetail{Event: entity.Event{ID: id, Published: r.published}}, nil
}

// TestEventReads_AdminDetailIsFreshWhilePublicStaysCached pins the routing of newBookingModule: the admin detail must
// read the source directly (read-your-writes after publish, unpublish, cancel and PATCH), while the public detail keeps
// the 2 s cache.
func TestEventReads_AdminDetailIsFreshWhilePublicStaysCached(t *testing.T) {
	const id = "11111111-1111-4111-8111-111111111111"
	ctx := context.Background()
	clk := clock.Fixed{T: time.Date(2026, 11, 13, 9, 0, 0, 0, time.UTC)}
	src := &publishRepo{published: true}

	catalog, admin := newEventReads(src, clk)
	publicUC := usecase.NewGetEventDetail(catalog, clk)
	adminUC := usecase.NewGetEventDetail(admin, clk)

	d, err := publicUC.GetEventDetail(ctx, id)
	require.NoError(t, err)
	assert.True(t, d.Published)

	// An unpublish commits; the next admin read must show it at once.
	src.published = false
	a, err := adminUC.GetAdminEventDetail(ctx, id)
	require.NoError(t, err)
	assert.False(t, a.Published, "admin detail must not be served from the cache")

	// The public read is still inside its 2 s budget, so it keeps the earlier value.
	d, err = publicUC.GetEventDetail(ctx, id)
	require.NoError(t, err)
	assert.True(t, d.Published, "public detail is served from the cache within its TTL")
}
