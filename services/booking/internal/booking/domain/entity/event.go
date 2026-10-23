// Package entity holds the pure Booking domain model. It depends only on the standard library and the
// domain value objects; no SQL, HTTP, or framework types leak in here (structure.md).
package entity

import (
	"time"

	vo "github.com/tiket-sampai-nonton/rusuh-booking-be/services/booking/internal/booking/domain/valueobject"
)

// EventStatus is the sales lifecycle of an event (matches events.status CHECK).
type EventStatus string

// Event statuses.
const (
	EventDraft      EventStatus = "DRAFT"
	EventNotStarted EventStatus = "NOT_STARTED"
	EventAvailable  EventStatus = "AVAILABLE"
	EventSoldOut    EventStatus = "SOLD_OUT"
	EventClosed     EventStatus = "CLOSED"
)

// Event is the catalog + config aggregate (events table).
type Event struct {
	ID            string
	Slug          string
	Name          string
	Venue         string
	City          string // SRS-F-28-02: filter kota (migrasi 0003)
	Timezone      string
	StartsAt      time.Time
	SalesStartsAt time.Time
	SalesEndsAt   time.Time
	Status        EventStatus

	// Image URLs (SDD-BOOKING App. C.5, migrasi 0003); bisa kosong sebelum di-seed.
	ImageURL     string
	BannerURL    string
	ThumbnailURL string

	MaxTicketsPerOrder    int
	MaxTicketsPerBuyerNik int
	AllowMixedCategories  bool

	SelectionTTLSeconds  int
	PaymentTTLSeconds    int
	ClaimWindowSeconds   int
	MaxAttendeeChecks    int
	SelectionEditEnabled bool
	MaxSelectionEdits    int
	SubmitGraceSeconds   int

	// MinPriceMinor is the catalog minimum price across categories (derived, not a column).
	MinPriceMinor int64
}

// Category is a ticket tier (ticket_categories table).
type Category struct {
	ID         string
	EventID    string
	Code       string
	Name       string
	PriceMinor int64
	Currency   string
	Order      int
}

// Inventory is the per-category stock counter (category_inventories table).
type Inventory struct {
	CategoryID   string
	EventID      string
	Capacity     int
	HeldQuantity int
	SoldQuantity int
}

// Available returns the units a buyer may still hold (capacity minus held minus sold).
func (i Inventory) Available() int {
	a := i.Capacity - i.HeldQuantity - i.SoldQuantity
	if a < 0 {
		return 0
	}
	return a
}

// CategoryDetail pairs a category with its live availability for the event-detail DTO.
type CategoryDetail struct {
	Category  Category
	Available int
}

// EventDetail is an event plus its categories with availability (read model for GET detail).
type EventDetail struct {
	Event      Event
	Categories []CategoryDetail
}

// Supply is the event-level stock counter (event_supply table).
type Supply struct {
	EventID        string
	TotalCapacity  int
	AllocatedUnits int
	HeldUnits      int
	SoldUnits      int
	Version        vo.Version
}

// Free returns the units not yet allocated, held, or sold.
func (s Supply) Free() int {
	f := s.TotalCapacity - s.AllocatedUnits - s.HeldUnits - s.SoldUnits
	if f < 0 {
		return 0
	}
	return f
}

// SoldOut reports whether no free units remain.
func (s Supply) SoldOut() bool { return s.Free() == 0 }
