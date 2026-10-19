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

// CheckHealth implements inbound.IHealthUsecase.
type CheckHealth struct {
	service string
	checker outbound.IDependencyChecker
	clock   clock.IClock
}

// NewCheckHealth accepts interfaces and returns the concrete usecase.
func NewCheckHealth(service string, checker outbound.IDependencyChecker, clk clock.IClock) *CheckHealth {
	return &CheckHealth{service: service, checker: checker, clock: clk}
}

// CheckHealth pings every dependency and maps the result to the response DTO.
func (u *CheckHealth) CheckHealth(ctx context.Context) dto.HealthResponse {
	return mapper.ToHealthResponse(entity.NewHealth(u.service, u.checker.CheckAll(ctx), u.clock.Now()))
}
