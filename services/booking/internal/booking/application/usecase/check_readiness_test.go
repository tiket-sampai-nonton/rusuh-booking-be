package usecase

import (
	"context"
	"testing"
	"time"

	"github.com/tiket-sampai-nonton/rusuh-booking-be/pkg/clock"

	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/booking/internal/booking/domain/dto"
	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/booking/internal/booking/domain/entity"
	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/booking/internal/booking/domain/valueobject"
	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/booking/internal/booking/port/inbound"
)

// Fake sederhana untuk contoh. Setelah `make mock`, test usecase sebaiknya memakai mock dari _mock/.
type fakeChecker struct{ statuses []entity.DependencyStatus }

func (f fakeChecker) CheckAll(context.Context) []entity.DependencyStatus { return f.statuses }

var _ inbound.IReadinessUsecase = (*CheckReadiness)(nil)

func TestCheckReadiness(t *testing.T) {
	pg, _ := valueobject.NewDependencyName("postgres")
	at := time.Date(2026, 10, 20, 2, 0, 0, 0, time.UTC)

	tests := []struct {
		name       string
		statuses   []entity.DependencyStatus
		wantStatus string
		wantDep    string
	}{
		{"all up", []entity.DependencyStatus{{Name: pg, Reachable: true, Latency: 3 * time.Millisecond}}, dto.StatusReady, dto.StatusUp},
		{"postgres down", []entity.DependencyStatus{{Name: pg, Reason: "connection refused"}}, dto.StatusNotReady, dto.StatusDown},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			uc := NewCheckReadiness("booking", fakeChecker{statuses: tt.statuses}, clock.Fixed{T: at})

			got, err := uc.CheckReadiness(context.Background())

			if err != nil {
				t.Fatalf("CheckReadiness() error = %v", err)
			}
			if got.Status != tt.wantStatus || got.Service != "booking" || got.CheckedAt != "2026-10-20T02:00:00Z" {
				t.Fatalf("got %+v", got)
			}
			if len(got.Dependencies) != 1 || got.Dependencies[0].Status != tt.wantDep {
				t.Fatalf("dependencies = %+v", got.Dependencies)
			}
		})
	}
}
