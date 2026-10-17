// Package inbound contains the driving ports (usecase interfaces) of the Booking domain.
package inbound

import (
	"context"

	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/booking/internal/booking/domain/dto"
)

//go:generate go run go.uber.org/mock/mockgen@v0.6.0 -source=readiness_usecase.go -destination=../../../../_mock/booking/port/inbound/readiness_usecase_mock.go -package=mock_inbound

// IReadinessUsecase reports whether the service and its dependencies are ready.
type IReadinessUsecase interface {
	CheckReadiness(ctx context.Context) (dto.ReadinessResponse, error)
}
