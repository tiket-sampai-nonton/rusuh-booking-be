// Package outbound contains the driven ports (repository, client, gateway interfaces) of the Booking domain.
package outbound

import (
	"context"

	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/booking/internal/booking/domain/entity"
)

//go:generate go tool mockgen -source=dependency_checker.go -destination=../../../../_mock/booking/port/outbound/dependency_checker_mock.go -package=mock_outbound

// IDependencyChecker pings infrastructure dependencies.
type IDependencyChecker interface {
	CheckAll(ctx context.Context) []entity.DependencyStatus
}
