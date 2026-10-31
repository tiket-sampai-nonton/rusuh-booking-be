package usecase_test

import (
	"encoding/json"
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tiket-sampai-nonton/rusuh-booking-be/pkg/contract"

	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/notification/internal/notification/domain/dto"
)

const events = "../../../../../../docs/contracts/events/"

// consumer is a payload DTO that can validate itself.
type consumer interface{ Validate() error }

// Platform 12.2 (consumer side). For every event Notification consumes: the fixture must follow the schema (so the
// fixture is trustworthy), the DTO must accept it, and encoding the DTO again must still follow the schema. That last
// step fails when the DTO forgets a field the contract requires.
func TestConsumedEvents_DTOsCoverTheirContracts(t *testing.T) {
	cases := []struct {
		schema  string
		fixture string
		into    func() (any, consumer)
	}{
		{"booking.paid.v1.schema.json", `{
			"bookingId":"11111111-1111-1111-1111-111111111111","publicRef":"RB-7F3A9C21","buyerEmailEnc":"c2VhbGVk",
			"event":{"eventId":"22222222-2222-2222-2222-222222222222","name":"Konser A","startsAt":"2026-12-25T19:00:00Z","venue":"GBK"},
			"tickets":[{"ticketCode":"TK-AAAA1111","attendeeName":"Budi","attendeeNikMasked":"3174********0001","categoryCode":"VIP"}],
			"ticketLinkToken":"opaque"}`,
			func() (any, consumer) { p := &dto.BookingPaidPayload{}; return p, p }},
		{"booking.expired.v1.schema.json", `{"bookingId":"11111111-1111-1111-1111-111111111111","publicRef":"RB-7F3A9C21","buyerEmailEnc":"c2VhbGVk","reason":"PAYMENT_TIMEOUT"}`,
			func() (any, consumer) { p := &dto.BookingExpiredPayload{}; return p, p }},
		{"booking.review.v1.schema.json", `{"bookingId":"11111111-1111-1111-1111-111111111111","buyerEmailEnc":"c2VhbGVk","amountMinor":1500000}`,
			func() (any, consumer) { p := &dto.BookingReviewPayload{}; return p, p }},
		{"queue.turn.soon.v1.schema.json", `{"eventId":"22222222-2222-2222-2222-222222222222","queueEntryId":"33333333-3333-3333-3333-333333333333","visitorId":"44444444-4444-4444-4444-444444444444"}`,
			func() (any, consumer) { p := &dto.TurnSoonPayload{}; return p, p }},
		{"queue.turn.arrived.v1.schema.json", `{"eventId":"22222222-2222-2222-2222-222222222222","queueEntryId":"33333333-3333-3333-3333-333333333333","visitorId":"44444444-4444-4444-4444-444444444444","claimDeadline":"2026-10-26T10:05:00Z"}`,
			func() (any, consumer) { p := &dto.TurnArrivedPayload{}; return p, p }},
		{"otp.requested.v1.schema.json", `{"emailEnc":"abc","codeEnc":"def","expiresAt":"2026-10-26T10:05:00Z","locale":"id"}`,
			func() (any, consumer) { p := &dto.OTPRequestedPayload{}; return p, p }},
	}
	for _, c := range cases {
		t.Run(c.schema, func(t *testing.T) {
			schema := events + c.schema
			require.NoError(t, contract.Validate(mustRead(t, schema), []byte(c.fixture)), "fixture harus sesuai schema")

			ptr, v := c.into()
			require.NoError(t, json.Unmarshal([]byte(c.fixture), ptr))
			assert.NoError(t, v.Validate(), "DTO menerima payload yang sesuai kontrak")
			assert.NoError(t, contract.ValidateFile(schema, ptr), "DTO membaca semua field wajib kontrak")
		})
	}
}

func mustRead(t *testing.T, path string) []byte {
	t.Helper()
	raw, err := os.ReadFile(path)
	require.NoError(t, err)
	return raw
}

// The contract no longer requires ticketLinkToken (nobody issues or uses it yet): the payload Booking sends must be
// valid and accepted, and the e-ticket email must not print an empty link line.
func TestConsumedEvents_BookingPaidWithoutTicketLinkToken(t *testing.T) {
	const fixture = `{
		"bookingId":"11111111-1111-1111-1111-111111111111","publicRef":"RB-7F3A9C21","buyerEmailEnc":"c2VhbGVk",
		"event":{"eventId":"22222222-2222-2222-2222-222222222222","name":"Konser A","startsAt":"2026-12-25T19:00:00Z","venue":"GBK"},
		"tickets":[{"ticketCode":"TK-AAAA1111","attendeeName":"Budi","attendeeNikMasked":"3174********0001","categoryCode":"VIP"}]}`
	require.NoError(t, contract.Validate(mustRead(t, events+"booking.paid.v1.schema.json"), []byte(fixture)))

	var p dto.BookingPaidPayload
	require.NoError(t, json.Unmarshal([]byte(fixture), &p))
	assert.NoError(t, p.Validate())
	assert.Empty(t, p.TicketLinkToken)
}
