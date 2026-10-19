// Package entity contains the Queue domain objects, constants, and errors.
package entity

import (
	"time"

	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/queue/internal/queue/domain/valueobject"
)

// DependencyStatus is the result of pinging one dependency.
type DependencyStatus struct {
	Name    valueobject.DependencyName
	Up      bool
	Latency time.Duration
	Reason  string
}

// Health is the aggregated health of the service (GET /health, D-23).
type Health struct {
	Service      string
	Up           bool
	Dependencies []DependencyStatus
	CheckedAt    time.Time
}

// NewHealth aggregates dependency results. The service is up only when every dependency is up.
func NewHealth(service string, deps []DependencyStatus, at time.Time) Health {
	up := true
	for _, d := range deps {
		if !d.Up {
			up = false
			break
		}
	}
	return Health{Service: service, Up: up, Dependencies: deps, CheckedAt: at}
}
