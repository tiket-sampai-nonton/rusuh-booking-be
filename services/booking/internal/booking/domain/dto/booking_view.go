package dto

import "time"

// AttendeeInput is the attendee portion of a hold/replace item.
type AttendeeInput struct {
	NIK            string `json:"nik,omitempty"`
	FullName       string `json:"fullName,omitempty"`
	KeepFromItemID string `json:"keepFromItemId,omitempty"`
}

// HoldItem is one requested ticket line.
type HoldItem struct {
	CategoryID string        `json:"categoryId"`
	Attendee   AttendeeInput `json:"attendee"`
}

// HoldRequest is the POST /api/v1/bookings body.
type HoldRequest struct {
	SessionID string     `json:"sessionId"`
	Items     []HoldItem `json:"items"`
}

// ReplaceItemsRequest is the PUT /api/v1/bookings/{id}/items body.
type ReplaceItemsRequest struct {
	Version int        `json:"version"`
	Items   []HoldItem `json:"items"`
}

// BookingItemView is one item in a booking response.
type BookingItemView struct {
	ItemID            string `json:"itemId"`
	CategoryID        string `json:"categoryId"`
	AttendeeNIKMasked string `json:"attendeeNikMasked"`
	AttendeeName      string `json:"attendeeName"`
}

// Booking is the booking response body.
type Booking struct {
	BookingID        string            `json:"bookingId"`
	PublicRef        string            `json:"publicRef"`
	Status           string            `json:"status"`
	AmountMinor      int64             `json:"amountMinor"`
	Currency         string            `json:"currency"`
	PaymentExpiresAt time.Time         `json:"paymentExpiresAt"`
	ServerTime       time.Time         `json:"serverTime"`
	Version          int               `json:"version"`
	CanEdit          bool              `json:"canEdit"`
	EditsRemaining   int               `json:"editsRemaining,omitempty"`
	LockReason       string            `json:"lockReason,omitempty"`
	Items            []BookingItemView `json:"items"`
}

// TicketView is one e-ticket.
type TicketView struct {
	TicketCode        string `json:"ticketCode"`
	CategoryCode      string `json:"categoryCode"`
	AttendeeName      string `json:"attendeeName"`
	AttendeeNIKMasked string `json:"attendeeNikMasked"`
	QRPayload         string `json:"qrPayload"`
}

// TicketList is the GET /api/v1/bookings/{id}/tickets response.
type TicketList struct {
	BookingID string       `json:"bookingId"`
	Tickets   []TicketView `json:"tickets"`
}

// MyBookingItem is one row of the buyer's booking history.
type MyBookingItem struct {
	BookingID   string    `json:"bookingId"`
	PublicRef   string    `json:"publicRef"`
	EventName   string    `json:"eventName"`
	Status      string    `json:"status"`
	AmountMinor int64     `json:"amountMinor"`
	CreatedAt   time.Time `json:"createdAt"`
}

// MyBookings is the GET /api/v1/me/bookings response.
type MyBookings struct {
	Bookings   []MyBookingItem `json:"bookings"`
	NextCursor *string         `json:"nextCursor"`
}
