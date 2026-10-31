// Package entity holds the pure domain model of the Payment Service (standard library and value objects only).
package entity

import (
	"time"

	vo "github.com/tiket-sampai-nonton/rusuh-booking-be/services/payment/internal/payment/domain/valueobject"
)

// Provider is the only payment provider (Midtrans is mandatory).
const Provider = "MIDTRANS"

// Instruction is the Snap hand-off stored with an attempt (Type is always SNAP). The token never leaves the backend response.
type Instruction struct {
	Type             string `json:"type"`
	SnapToken        string `json:"snapToken,omitempty"`
	RedirectURL      string `json:"redirectUrl,omitempty"`
	SuccessReturnURL string `json:"successReturnUrl,omitempty"`
	FailedReturnURL  string `json:"failedReturnUrl,omitempty"`
}

// Attempt is one try to pay a booking with one method. payment_attempts is the source of truth for its status.
type Attempt struct {
	ID                 string
	BookingID          string
	AttemptNo          int
	Method             vo.Method
	ProviderOrderID    string
	ProviderTxID       string
	Status             vo.AttemptStatus
	AmountMinor        int64
	ExpiresAt          time.Time
	Instruction        *Instruction
	SettlementTime     *time.Time
	LastProviderStatus string
	BookingVersion     int
}
