// Package entity holds the pure domain model of the Payment Service (standard library and value objects only).
package entity

import (
	"time"

	vo "github.com/tiket-sampai-nonton/rusuh-booking-be/services/payment/internal/payment/domain/valueobject"
)

// Provider is the only payment provider (Midtrans is mandatory).
const Provider = "MIDTRANS"

// Instruction tells the customer how to pay. Type is VA, QR, or DEEPLINK (payment.yaml).
type Instruction struct {
	Type        string `json:"type"`
	Bank        string `json:"bank,omitempty"`
	VANumber    string `json:"vaNumber,omitempty"`
	QRURL       string `json:"qrUrl,omitempty"`
	DeeplinkURL string `json:"deeplinkUrl,omitempty"`
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
