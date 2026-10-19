package mapper

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/user/internal/user/domain/dto"
	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/user/internal/user/domain/entity"
	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/user/internal/user/domain/valueobject"
)

func TestToHealthResponse(t *testing.T) {
	pg, err := valueobject.NewDependencyName("postgres")
	require.NoError(t, err)
	at := time.Date(2026, 10, 19, 7, 0, 0, 0, time.FixedZone("WIB", 7*3600))

	up := ToHealthResponse(entity.NewHealth("user", []entity.DependencyStatus{{Name: pg, Up: true, Latency: 3 * time.Millisecond}}, at))
	assert.Equal(t, dto.StatusUp, up.Status)
	assert.Equal(t, "2026-10-19T00:00:00Z", up.CheckedAt)
	assert.Equal(t, []dto.DependencyResponse{{Name: "postgres", Status: dto.StatusUp, LatencyMs: 3}}, up.Dependencies)

	down := ToHealthResponse(entity.NewHealth("user", []entity.DependencyStatus{{Name: pg, Reason: "timeout"}}, at))
	assert.Equal(t, dto.StatusDown, down.Status)
	assert.Equal(t, "timeout", down.Dependencies[0].Reason)
}
