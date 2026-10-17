// Package entity contains the Payment domain objects, constants, and errors.
package entity

import (
	"time"

	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/payment/internal/payment/domain/valueobject"
)

// DependencyStatus is the result of checking one dependency.
type DependencyStatus struct {
	Name      valueobject.DependencyName
	Reachable bool
	Latency   time.Duration
	Reason    string
}

// Readiness is the aggregated readiness of the service.
type Readiness struct {
	Service      string
	Ready        bool
	Dependencies []DependencyStatus
	CheckedAt    time.Time
}

// NewReadiness aggregates dependency results. The service is ready only when every dependency is reachable.
func NewReadiness(service string, deps []DependencyStatus, at time.Time) Readiness {
	ready := true
	for _, d := range deps {
		if !d.Reachable {
			ready = false
			break
		}
	}
	return Readiness{Service: service, Ready: ready, Dependencies: deps, CheckedAt: at}
}
