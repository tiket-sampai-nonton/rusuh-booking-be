// Package inbound contains the driving ports (usecase interfaces) of the Queue domain.
package inbound

import (
	"context"

	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/queue/internal/queue/domain/dto"
)

//go:generate go run go.uber.org/mock/mockgen@v0.6.0 -source=readiness_usecase.go -destination=../../../../_mock/queue/port/inbound/readiness_usecase_mock.go -package=mock_inbound

// IReadinessUsecase reports whether the service and its dependencies are ready.
type IReadinessUsecase interface {
	CheckReadiness(ctx context.Context) (dto.ReadinessResponse, error)
}
