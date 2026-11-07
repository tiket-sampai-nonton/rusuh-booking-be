package dto

import "time"

// ReleaseRequest is the body of POST /internal/v1/allocations/{id}:release.
type ReleaseRequest struct {
	Reason string `json:"reason"` // QUEUE_ENTRY_GONE
}

// ReleaseResponse is the outcome of releasing an allocation.
type ReleaseResponse struct {
	Released bool `json:"released"`
}

// SupplySnapshot is the stock view returned by Booking to the Queue Service.
type SupplySnapshot struct {
	EventID          string           `json:"eventId"`
	RemainingTickets int              `json:"remainingTickets"`
	SoldOut          bool             `json:"soldOut"`
	Categories       []SupplyCategory `json:"categories"`

	// Event-level ledger and sale window (additive, ADR-030): the Queue sizes admissions (3 units each),
	// the status zones and the opening window from these.
	AllocatedUnits     int       `json:"allocatedUnits"`
	HeldUnits          int       `json:"heldUnits"`
	FreeUnits          int       `json:"freeUnits"`
	MaxTicketsPerOrder int       `json:"maxTicketsPerOrder"`
	SalesStartsAt      time.Time `json:"salesStartsAt"`
	SalesEndsAt        time.Time `json:"salesEndsAt"`
}

// SupplyCategory is per-category stock for the Queue status publisher.
type SupplyCategory struct {
	Code  string `json:"code"`
	Name  string `json:"name"`
	Sold  int    `json:"sold"`
	Total int    `json:"total"`
}
