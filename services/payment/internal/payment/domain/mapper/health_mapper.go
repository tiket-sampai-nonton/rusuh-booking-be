// Package mapper contains pure transformations between domain entities and DTOs.
package mapper

import (
	"time"

	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/payment/internal/payment/domain/dto"
	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/payment/internal/payment/domain/entity"
)

// ToHealthResponse maps the health entity to its response DTO.
func ToHealthResponse(h entity.Health) dto.HealthResponse {
	out := dto.HealthResponse{
		Status:       dto.StatusDown,
		Service:      h.Service,
		CheckedAt:    h.CheckedAt.UTC().Format(time.RFC3339),
		Dependencies: make([]dto.DependencyResponse, 0, len(h.Dependencies)),
	}
	if h.Up {
		out.Status = dto.StatusUp
	}
	for _, d := range h.Dependencies {
		dep := dto.DependencyResponse{Name: d.Name.String(), Status: dto.StatusDown, LatencyMs: d.Latency.Milliseconds(), Reason: d.Reason}
		if d.Up {
			dep.Status = dto.StatusUp
		}
		out.Dependencies = append(out.Dependencies, dep)
	}
	return out
}
