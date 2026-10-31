package usecase_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/booking/internal/booking/application/usecase"
	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/booking/internal/booking/port/outbound"
)

type fakeAvailSource struct {
	byEvent map[string][]outbound.CategoryAvailabilitySnap
	err     error
}

func (f fakeAvailSource) OnSaleAvailability(context.Context, time.Time) (map[string][]outbound.CategoryAvailabilitySnap, error) {
	return f.byEvent, f.err
}

func TestProjectAvailability_WritesEverySnapshotAndReportsFailures(t *testing.T) {
	snaps := map[string][]outbound.CategoryAvailabilitySnap{
		"ev1": {{CategoryID: "c1", Available: 3}},
		"ev2": {{CategoryID: "c2", Available: 0}},
	}
	cache := &fakeAvailCache{}
	uc := usecase.NewProjectAvailability(fakeAvailSource{byEvent: snaps}, cache, fixedClock{})
	require.NoError(t, uc.ProjectAvailability(context.Background()))
	assert.Equal(t, snaps, cache.written)

	cache = &fakeAvailCache{setErr: errors.New("valkey down")}
	uc = usecase.NewProjectAvailability(fakeAvailSource{byEvent: snaps}, cache, fixedClock{})
	assert.ErrorContains(t, uc.ProjectAvailability(context.Background()), "valkey down")

	uc = usecase.NewProjectAvailability(fakeAvailSource{err: errors.New("db down")}, &fakeAvailCache{}, fixedClock{})
	assert.ErrorContains(t, uc.ProjectAvailability(context.Background()), "db down")
}
