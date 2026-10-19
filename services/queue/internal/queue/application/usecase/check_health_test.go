package usecase

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	"github.com/tiket-sampai-nonton/rusuh-booking-be/pkg/clock"

	mock_outbound "github.com/tiket-sampai-nonton/rusuh-booking-be/services/queue/_mock/queue/port/outbound"
	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/queue/internal/queue/domain/dto"
	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/queue/internal/queue/domain/entity"
	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/queue/internal/queue/domain/valueobject"
)

func TestCheckHealth(t *testing.T) {
	pg, err := valueobject.NewDependencyName("postgres")
	require.NoError(t, err)
	at := time.Date(2026, 10, 19, 0, 0, 0, 0, time.UTC)

	tests := []struct {
		name string
		deps []entity.DependencyStatus
		want string
	}{
		{"up", []entity.DependencyStatus{{Name: pg, Up: true}}, dto.StatusUp},
		{"down", []entity.DependencyStatus{{Name: pg, Reason: "refused"}}, dto.StatusDown},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctrl := gomock.NewController(t)
			checker := mock_outbound.NewMockIDependencyChecker(ctrl)
			checker.EXPECT().CheckAll(gomock.Any()).Return(tt.deps)

			got := NewCheckHealth("queue", checker, clock.Fixed{T: at}).CheckHealth(context.Background())
			assert.Equal(t, tt.want, got.Status)
			assert.Equal(t, "queue", got.Service)
			assert.Len(t, got.Dependencies, 1)
		})
	}
}
