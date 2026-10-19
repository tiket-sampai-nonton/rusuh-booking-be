// Package inbound contains the driving ports (usecase interfaces) of the Notification domain.
package inbound

import (
	"context"

	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/notification/internal/notification/domain/dto"
)

//go:generate go tool mockgen -source=health_usecase.go -destination=../../../../_mock/notification/port/inbound/health_usecase_mock.go -package=mock_inbound

// IHealthUsecase reports whether the service and its dependencies are healthy.
type IHealthUsecase interface {
	CheckHealth(ctx context.Context) dto.HealthResponse
}
