// Package dto contains the request/response DTOs of this domain (consolidated).
package dto

// HealthResponse is returned by GET /health.
type HealthResponse struct {
	Status       string               `json:"status"`
	Service      string               `json:"service"`
	CheckedAt    string               `json:"checkedAt"`
	Dependencies []DependencyResponse `json:"dependencies"`
}

// DependencyResponse is one dependency in HealthResponse.
type DependencyResponse struct {
	Name      string `json:"name"`
	Status    string `json:"status"`
	LatencyMs int64  `json:"latencyMs"`
	Reason    string `json:"reason,omitempty"`
}

// Status values.
const (
	StatusUp   = "UP"
	StatusDown = "DOWN"
)
