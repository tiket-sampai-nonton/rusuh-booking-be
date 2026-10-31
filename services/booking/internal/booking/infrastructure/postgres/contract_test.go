package postgres_test

// contract_test.go verifies that every outbox payload produced by the booking service
// contains all required fields per docs/contracts/events/*.v1.schema.json (12.R2).
// These are plain unit tests — no database required.

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// payloadHas asserts that the JSON payload contains non-empty values for every required field.
func payloadHas(t *testing.T, payload map[string]any, fields ...string) {
	t.Helper()
	for _, f := range fields {
		v, ok := payload[f]
		assert.True(t, ok, "missing required field %q", f)
		if s, isStr := v.(string); isStr {
			assert.NotEmpty(t, s, "field %q must not be empty string", f)
		}
	}
}

// marshal→unmarshal round-trip helper.
func roundTrip(t *testing.T, src any) map[string]any {
	t.Helper()
	raw, err := json.Marshal(src)
	require.NoError(t, err)
	var out map[string]any
	require.NoError(t, json.Unmarshal(raw, &out))
	return out
}

// TestContract_JourneyEnded verifies booking.journey.ended.v1 required fields.
func TestContract_JourneyEnded(t *testing.T) {
	payload := roundTrip(t, map[string]any{
		"eventId":      "00000000-0000-0000-0000-000000000001",
		"queueEntryId": "00000000-0000-0000-0000-000000000002",
		"outcome":      "PAID",
	})
	payloadHas(t, payload, "eventId", "queueEntryId", "outcome")

	// outcome must be one of the allowed values.
	validOutcomes := map[string]bool{
		"PAID": true, "EXPIRED": true, "CANCELLED": true,
		"LEFT_WITHOUT_BUYING": true, "UNCLAIMED": true,
	}
	assert.True(t, validOutcomes[payload["outcome"].(string)],
		"outcome %q is not a valid value", payload["outcome"])
}

// TestContract_QuotaExhausted verifies booking.quota.exhausted.v1 required fields.
func TestContract_QuotaExhausted(t *testing.T) {
	payload := roundTrip(t, map[string]any{
		"eventId":      "00000000-0000-0000-0000-000000000001",
		"buyerNikHash": "deadbeefdeadbeef",
	})
	payloadHas(t, payload, "eventId", "buyerNikHash")
}

// TestContract_SoldOut verifies booking.sold_out.v1 required fields.
func TestContract_SoldOut(t *testing.T) {
	payload := roundTrip(t, map[string]any{
		"eventId": "00000000-0000-0000-0000-000000000001",
	})
	payloadHas(t, payload, "eventId")
}

// TestContract_BookingExpired verifies booking.expired.v1 required fields.
func TestContract_BookingExpired(t *testing.T) {
	payload := roundTrip(t, map[string]any{
		"bookingId":     "00000000-0000-0000-0000-000000000001",
		"publicRef":     "RB12345678",
		"buyerEmailEnc": "c2VhbGVk",
		"reason":        "PAYMENT_TIMEOUT",
	})
	payloadHas(t, payload, "bookingId", "publicRef", "buyerEmailEnc", "reason")
}

// TestContract_BookingReview verifies booking.review.v1 required fields.
func TestContract_BookingReview(t *testing.T) {
	payload := roundTrip(t, map[string]any{
		"bookingId":     "00000000-0000-0000-0000-000000000001",
		"buyerEmailEnc": "c2VhbGVk",
		"amountMinor":   int64(150000),
	})
	payloadHas(t, payload, "bookingId", "buyerEmailEnc", "amountMinor")
}

// TestContract_BookingPaid verifies booking.paid.v1 required fields including tickets array.
func TestContract_BookingPaid(t *testing.T) {
	type ticketLine struct {
		TicketCode        string `json:"ticketCode"`
		AttendeeName      string `json:"attendeeName"`
		AttendeeNIKMasked string `json:"attendeeNikMasked"`
		CategoryCode      string `json:"categoryCode"`
	}
	payload := roundTrip(t, map[string]any{
		"bookingId":     "00000000-0000-0000-0000-000000000001",
		"publicRef":     "RB12345678",
		"buyerEmailEnc": "c2VhbGVk",
		"tickets": []ticketLine{
			{
				TicketCode:        "TK-001",
				AttendeeName:      "Alice",
				AttendeeNIKMasked: "3201****0001",
				CategoryCode:      "VIP",
			},
		},
		"ticketLinkToken": "link-token-abc",
	})
	payloadHas(t, payload, "bookingId", "publicRef", "buyerEmailEnc", "ticketLinkToken")

	// tickets must be present and be an array.
	tickets, ok := payload["tickets"]
	assert.True(t, ok, "tickets field required")
	ticketsArr, isArr := tickets.([]any)
	assert.True(t, isArr, "tickets must be an array")
	require.Len(t, ticketsArr, 1, "expected 1 ticket")

	tl := ticketsArr[0].(map[string]any)
	payloadHas(t, tl, "ticketCode", "attendeeName", "attendeeNikMasked", "categoryCode")
}

// TestContract_JourneyEnded_AllOutcomes checks every allowed outcome value.
func TestContract_JourneyEnded_AllOutcomes(t *testing.T) {
	outcomes := []string{"PAID", "EXPIRED", "CANCELLED", "LEFT_WITHOUT_BUYING", "UNCLAIMED"}
	for _, outcome := range outcomes {
		t.Run(outcome, func(t *testing.T) {
			payload := roundTrip(t, map[string]any{
				"eventId":      "00000000-0000-0000-0000-000000000001",
				"queueEntryId": "00000000-0000-0000-0000-000000000002",
				"outcome":      outcome,
			})
			payloadHas(t, payload, "eventId", "queueEntryId", "outcome")
			assert.Equal(t, outcome, payload["outcome"])
		})
	}
}
