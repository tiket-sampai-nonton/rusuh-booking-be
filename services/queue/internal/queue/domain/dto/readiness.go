// Package dto contains the request/response DTOs of this domain (consolidated).
package dto

// ReadinessResponse is returned by GET /readyz.
type ReadinessResponse struct {
	Status       string               `json:"status"`
	Service      string               `json:"service"`
	CheckedAt    string               `json:"checkedAt"`
	Dependencies []DependencyResponse `json:"dependencies"`
}

// DependencyResponse is one dependency in ReadinessResponse.
type DependencyResponse struct {
	Name      string `json:"name"`
	Status    string `json:"status"`
	LatencyMs int64  `json:"latencyMs"`
	Reason    string `json:"reason,omitempty"`
}

// Status values.
const (
	StatusReady    = "ready"
	StatusNotReady = "not_ready"
	StatusUp       = "up"
	StatusDown     = "down"
)
