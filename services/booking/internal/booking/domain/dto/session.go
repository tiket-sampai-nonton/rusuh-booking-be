package dto

import "time"

// CreateSessionRequest opens a booking session from a one-time admission token.
type CreateSessionRequest struct {
	AdmissionToken string `json:"admissionToken"`
}

// BookingSession is the session view returned to the client.
type BookingSession struct {
	SessionID            string         `json:"sessionId"`
	EventID              string         `json:"eventId"`
	Phase                string         `json:"phase"` // SELECTING | EDITABLE | LOCKED | ENDED
	ReservedUnits        int            `json:"reservedUnits"`
	MaxSelectable        int            `json:"maxSelectable"`
	BuyerNIKMasked       string         `json:"buyerNikMasked,omitempty"`
	AllowMixedCategories bool           `json:"allowMixedCategories"`
	ExpiresAt            time.Time      `json:"expiresAt"`
	ServerTime           time.Time      `json:"serverTime"`
	EditEnabled          bool           `json:"editEnabled"`
	MaxEdits             int            `json:"maxEdits"`
	Categories           []CategoryView `json:"categories"`
	Booking              *Booking       `json:"booking"`
}

// AllocationEntry is one admission reservation request from the Queue Service.
type AllocationEntry struct {
	QueueEntryID   string `json:"queueEntryId"`
	Seq            int64  `json:"seq"`
	VisitorID      string `json:"visitorId"`
	BuyerNIKHash   string `json:"buyerNikHash"` // hex HMAC
	BuyerNIKMasked string `json:"buyerNikMasked"`
	BuyerEmailEnc  string `json:"buyerEmailEnc"`
	AccountID      string `json:"accountId"`
	Units          int    `json:"units"`
}

// AllocationBatchRequest is the POST /internal/v1/allocations:batch body.
type AllocationBatchRequest struct {
	EventID string            `json:"eventId"`
	Entries []AllocationEntry `json:"entries"`
}

// AllocationResult is the outcome for one entry.
type AllocationResult struct {
	QueueEntryID  string     `json:"queueEntryId"`
	Outcome       string     `json:"outcome"` // GRANTED | NO_SUPPLY_NOW | SOLD_OUT | BUYER_QUOTA_EXHAUSTED | ALREADY_ACTIVE
	AllocationID  string     `json:"allocationId,omitempty"`
	Units         int        `json:"units,omitempty"`
	ClaimDeadline *time.Time `json:"claimDeadline,omitempty"`
}

// AllocationBatchResponse is the batch reply.
type AllocationBatchResponse struct {
	Results []AllocationResult `json:"results"`
}

// AttendeeCheckRequest probes whether an attendee NIK may be used.
type AttendeeCheckRequest struct {
	NIK string `json:"nik"`
}

// AttendeeCheck is the attendee-check reply.
type AttendeeCheck struct {
	Result          string `json:"result"` // AVAILABLE | ALREADY_REGISTERED | CHECK_LIMIT_REACHED
	ChecksRemaining int    `json:"checksRemaining"`
}
