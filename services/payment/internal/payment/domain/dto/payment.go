package dto

import "time"

// Attempt is payment.yaml Attempt.
type Attempt struct {
	AttemptNo      int        `json:"attemptNo"`
	Method         string     `json:"method"`
	Status         string     `json:"status"`
	BookingVersion int        `json:"bookingVersion"`
	ExpiresAt      *time.Time `json:"expiresAt,omitempty"`
	// Snap hand-off: the FE opens RedirectURL; Snap returns the payer to SuccessReturnURL or FailedReturnURL,
	// and the FE then verifies the result with GET /payments/{bookingId}/status.
	RedirectURL      string `json:"redirectUrl,omitempty"`
	SuccessReturnURL string `json:"successReturnUrl,omitempty"`
	FailedReturnURL  string `json:"failedReturnUrl,omitempty"`
}

// PaymentPage is payment.yaml PaymentPage.
type PaymentPage struct {
	BookingID        string    `json:"bookingId"`
	Methods          []string  `json:"methods"`
	AmountMinor      int64     `json:"amountMinor"`
	PaymentExpiresAt time.Time `json:"paymentExpiresAt"`
	ServerTime       time.Time `json:"serverTime"`
	CurrentAttempt   *Attempt  `json:"currentAttempt"`
	CanEdit          bool      `json:"canEdit"`
}

// PaymentStatus is payment.yaml PaymentStatus.
type PaymentStatus struct {
	BookingID        string    `json:"bookingId"`
	BookingStatus    string    `json:"bookingStatus"`
	OrderID          string    `json:"orderId,omitempty"`
	PaymentExpiresAt time.Time `json:"paymentExpiresAt"`
	Attempt          *Attempt  `json:"attempt"`
	ServerTime       time.Time `json:"serverTime"`
}

// PageToken is payment.yaml PageToken.
type PageToken struct {
	PageToken string    `json:"pageToken"`
	PageURL   string    `json:"pageUrl"`
	ExpiresAt time.Time `json:"expiresAt"`
}

// CreateAttemptRequest is payment.yaml CreateAttemptRequest.
type CreateAttemptRequest struct {
	Method string `json:"method"`
}

// PageSessionRequest is payment.yaml PageSessionRequest.
type PageSessionRequest struct {
	PageToken string `json:"pageToken"`
}

// PageSession is the rb_pay cookie value to set after a page token is exchanged.
type PageSession struct {
	Cookie    string
	ExpiresAt time.Time
}

// ResolveRequest and ResolveResponse are internal-payment.yaml.
type ResolveRequest struct {
	Mode string `json:"mode"`
}

// Resolve modes and outcomes.
const (
	ResolveCheck          = "CHECK"
	ResolveCheckAndCancel = "CHECK_AND_CANCEL"

	OutcomeSucceeded = "SUCCEEDED"
	OutcomeNotPaid   = "NOT_PAID"
	OutcomeNoAttempt = "NO_ATTEMPT"
	OutcomeUnknown   = "UNKNOWN"
)

// ResolveResponse is internal-payment.yaml ResolveResponse.
type ResolveResponse struct {
	Outcome        string     `json:"outcome"`
	SettlementTime *time.Time `json:"settlementTime,omitempty"`
	AttemptID      string     `json:"attemptId,omitempty"`
}

// MidtransNotification holds the fields of the HTTP notification the service reads (additionalProperties are ignored).
type MidtransNotification struct {
	OrderID           string `json:"order_id"`
	TransactionStatus string `json:"transaction_status"`
	StatusCode        string `json:"status_code"`
	GrossAmount       string `json:"gross_amount"`
	SignatureKey      string `json:"signature_key"`
	TransactionID     string `json:"transaction_id"`
	SettlementTime    string `json:"settlement_time"`
	FraudStatus       string `json:"fraud_status"`
}

// PaymentSucceeded and PaymentFailed are the event payloads (docs/contracts/events/payment.*.v1.schema.json).
type PaymentSucceeded struct {
	BookingID      string    `json:"bookingId"`
	AttemptID      string    `json:"attemptId"`
	SettlementTime time.Time `json:"settlementTime"`
	AmountMinor    int64     `json:"amountMinor"`
	Method         string    `json:"method"`
}

// PaymentFailed is the payload of payment.failed.
type PaymentFailed struct {
	BookingID string `json:"bookingId"`
	AttemptID string `json:"attemptId"`
	Reason    string `json:"reason"`
}
