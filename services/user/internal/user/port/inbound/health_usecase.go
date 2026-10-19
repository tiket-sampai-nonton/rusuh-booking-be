// Package inbound contains the driving ports (usecase interfaces) of the User domain.
package inbound

import (
	"context"

	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/user/internal/user/domain/dto"
)

//go:generate go tool mockgen -source=health_usecase.go -destination=../../../../_mock/user/port/inbound/health_usecase_mock.go -package=mock_inbound

// IHealthUsecase reports whether the service and its dependencies are healthy.
type IHealthUsecase interface {
	CheckHealth(ctx context.Context) dto.HealthResponse
}
