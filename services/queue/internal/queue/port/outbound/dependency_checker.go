// Package outbound contains the driven ports (repository, client, gateway interfaces) of the Queue domain.
package outbound

import (
	"context"

	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/queue/internal/queue/domain/entity"
)

//go:generate go run go.uber.org/mock/mockgen@v0.6.0 -source=dependency_checker.go -destination=../../../../_mock/queue/port/outbound/dependency_checker_mock.go -package=mock_outbound

// IDependencyChecker checks the reachability of infrastructure dependencies.
type IDependencyChecker interface {
	CheckAll(ctx context.Context) []entity.DependencyStatus
}
