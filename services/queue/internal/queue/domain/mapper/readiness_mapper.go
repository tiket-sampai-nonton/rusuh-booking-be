// Package mapper contains pure transformations between domain entities and DTOs.
package mapper

import (
	"time"

	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/queue/internal/queue/domain/dto"
	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/queue/internal/queue/domain/entity"
)

// ToReadinessResponse maps the readiness entity to its response DTO.
func ToReadinessResponse(r entity.Readiness) dto.ReadinessResponse {
	out := dto.ReadinessResponse{
		Status:       dto.StatusNotReady,
		Service:      r.Service,
		CheckedAt:    r.CheckedAt.UTC().Format(time.RFC3339),
		Dependencies: make([]dto.DependencyResponse, 0, len(r.Dependencies)),
	}
	if r.Ready {
		out.Status = dto.StatusReady
	}
	for _, d := range r.Dependencies {
		dep := dto.DependencyResponse{
			Name:      d.Name.String(),
			Status:    dto.StatusDown,
			LatencyMs: d.Latency.Milliseconds(),
			Reason:    d.Reason,
		}
		if d.Reachable {
			dep.Status = dto.StatusUp
		}
		out.Dependencies = append(out.Dependencies, dep)
	}
	return out
}
