package valueobject

import (
	"fmt"
	"strings"
	"time"
)

// wib is Western Indonesian Time. Midtrans documents order_time/custom_expiry in GMT+7 and does not state
// the zone of transaction_time/settlement_time; they are read as WIB too.
// shortcut: assumption taken from the docs examples, confirm with a real Sandbox settlement (payment Task 13).
var wib = time.FixedZone("WIB", 7*60*60)

// WIB returns the GMT+7 zone used by Midtrans timestamps.
func WIB() *time.Location { return wib }

// MapProviderStatus converts a Midtrans transaction_status (+ fraud_status) to an attempt status. ok=false
// means the status carries no payment progress (refund, chargeback, authorize, unknown) and must be ignored.
func MapProviderStatus(transactionStatus, fraudStatus string) (AttemptStatus, bool) {
	switch transactionStatus {
	case "pending":
		return AttemptPending, true
	case "settlement":
		return AttemptSucceeded, true
	case "capture": // card only; kept so a future card method needs no mapping change
		if fraudStatus == "" || fraudStatus == "accept" {
			return AttemptSucceeded, true
		}
		return "", false
	case "deny", "failure":
		return AttemptFailed, true
	case "cancel":
		return AttemptCancelled, true
	case "expire":
		return AttemptExpired, true
	}
	return "", false
}

// ParseSettlementTime reads a Midtrans "YYYY-MM-DD HH:MM:SS" timestamp as WIB and returns it in UTC.
func ParseSettlementTime(s string) (time.Time, error) {
	t, err := time.ParseInLocation("2006-01-02 15:04:05", strings.TrimSpace(s), wib)
	if err != nil {
		return time.Time{}, fmt.Errorf("waktu midtrans %q tidak valid: %w", s, err)
	}
	return t.UTC(), nil
}
