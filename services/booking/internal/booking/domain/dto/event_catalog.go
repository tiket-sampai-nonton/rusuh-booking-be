// Package dto holds the request/response shapes exchanged over HTTP. They mirror the OpenAPI contracts
// and are produced from entities by the mapper package (handlers never return entities).
package dto

import "time"

// EventListQuery is the parsed filter for GET /api/v1/events.
type EventListQuery struct {
	Timeframe string // ALL | THIS_WEEK | THIS_MONTH | UPCOMING
	City      string
	Genres    []string // US-015: cocok bila event punya salah satu genre (overlap); kosong = semua
	Status    string   // ALL | NOT_STARTED | AVAILABLE | SOLD_OUT | CLOSED
	Sort      string   // DATE_ASC | DATE_DESC | PRICE_ASC | PRICE_DESC

	// StartDate/EndDate are raw YYYY-MM-DD bounds from the client (inclusive, WIB). The usecase parses
	// and converts them to a half-open UTC window; the repo filters on starts_at (sargable), ANDed with Timeframe.
	StartDate string
	EndDate   string

	// startUTC/endUTC are the resolved half-open window [startUTC, endUTC) in UTC, set by the usecase.
	// Unexported-at-JSON intent but exported for the repo in the same module; nil = that bound is open.
	StartUTC *time.Time
	EndUTC   *time.Time
}

// EventSummary is one catalog row.
type EventSummary struct {
	EventID       string    `json:"eventId"`
	Slug          string    `json:"slug"`
	Name          string    `json:"name"`
	Venue         string    `json:"venue"`
	City          string    `json:"city"`
	Genres        []string  `json:"genres"` // selalu array ([] bila kosong), bukan omitempty
	StartsAt      time.Time `json:"startsAt"`
	SalesStartsAt time.Time `json:"salesStartsAt"`
	SalesEndsAt   time.Time `json:"salesEndsAt"`
	Status        string    `json:"status"`
	MinPriceMinor int64     `json:"minPriceMinor"`
	ImageURL      string    `json:"imageUrl,omitempty"`
	BannerURL     string    `json:"bannerUrl,omitempty"`
	ThumbnailURL  string    `json:"thumbnailUrl,omitempty"`
}

// EventList is the GET /api/v1/events response body.
type EventList struct {
	Events     []EventSummary `json:"events"`
	ServerTime time.Time      `json:"serverTime"`
}

// CategoryView is one category in the event detail.
type CategoryView struct {
	CategoryID string `json:"categoryId"`
	Code       string `json:"code"`
	Name       string `json:"name"`
	PriceMinor int64  `json:"priceMinor"`
	Available  int    `json:"available"`
}

// EventDetail is the GET /api/v1/events/{eventId} response body.
type EventDetail struct {
	Event                 EventSummary   `json:"event"`
	MaxTicketsPerOrder    int            `json:"maxTicketsPerOrder"`
	MaxTicketsPerBuyerNik int            `json:"maxTicketsPerBuyerNik"`
	AllowMixedCategories  bool           `json:"allowMixedCategories"`
	Categories            []CategoryView `json:"categories"`
}

// CreateCategoryRequest is one ticket category in a CreateEventRequest.
type CreateCategoryRequest struct {
	Code       string `json:"code"`
	Name       string `json:"name"`
	PriceMinor int64  `json:"priceMinor"`
	Capacity   int    `json:"capacity"`
}

// CreateEventRequest is the body of POST /internal/v1/admin/events.
type CreateEventRequest struct {
	Name                  string                  `json:"name"`
	Slug                  string                  `json:"slug"`
	Venue                 string                  `json:"venue"`
	StartsAt              time.Time               `json:"startsAt"`
	SalesStartsAt         time.Time               `json:"salesStartsAt"`
	SalesEndsAt           time.Time               `json:"salesEndsAt"`
	MaxTicketsPerOrder    int                     `json:"maxTicketsPerOrder"`
	MaxTicketsPerBuyerNIK int                     `json:"maxTicketsPerBuyerNik"`
	AllowMixedCategories  bool                    `json:"allowMixedCategories"`
	Categories            []CreateCategoryRequest `json:"categories"`
}

// UpdateEventConfigRequest is the body of PATCH /internal/v1/admin/events/{eventId}.
type UpdateEventConfigRequest struct {
	SelectionTTLSeconds *int  `json:"selectionTtlSeconds,omitempty"`
	PaymentTTLSeconds   *int  `json:"paymentTtlSeconds,omitempty"`
	EditEnabled         *bool `json:"editEnabled,omitempty"`
	MaxEdits            *int  `json:"maxEdits,omitempty"`
}
