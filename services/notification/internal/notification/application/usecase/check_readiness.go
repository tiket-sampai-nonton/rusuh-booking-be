// Package usecase orchestrates the Notification domain. One usecase per file.
package usecase

import (
	"context"

	"github.com/tiket-sampai-nonton/rusuh-booking-be/pkg/clock"

	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/notification/internal/notification/domain/dto"
	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/notification/internal/notification/domain/entity"
	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/notification/internal/notification/domain/mapper"
	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/notification/internal/notification/port/outbound"
)

// CheckReadiness implements inbound.IReadinessUsecase.
type CheckReadiness struct {
	service string
	checker outbound.IDependencyChecker
	clock   clock.IClock
}

// NewCheckReadiness accepts interfaces and returns the concrete usecase.
func NewCheckReadiness(service string, checker outbound.IDependencyChecker, clk clock.IClock) *CheckReadiness {
	return &CheckReadiness{service: service, checker: checker, clock: clk}
}

// CheckReadiness checks every dependency and maps the result to the response DTO.
func (u *CheckReadiness) CheckReadiness(ctx context.Context) (dto.ReadinessResponse, error) {
	statuses := u.checker.CheckAll(ctx)
	return mapper.ToReadinessResponse(entity.NewReadiness(u.service, statuses, u.clock.Now())), nil
}
