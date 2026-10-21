package dto_test

import (
	"encoding/base64"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/notification/internal/notification/domain/dto"
)

func TestBookingPaidPayload_Validate(t *testing.T) {
	ok := dto.BookingPaidPayload{BookingID: "b", PublicRef: "r", BuyerEmail: "e", Tickets: []dto.TicketInfo{{}}, TicketLinkToken: "t"}
	require.NoError(t, ok.Validate())
	require.Error(t, dto.BookingPaidPayload{}.Validate())
}

func TestBookingExpiredPayload_Validate(t *testing.T) {
	ok := dto.BookingExpiredPayload{BookingID: "b", PublicRef: "r", BuyerEmail: "e", Reason: dto.ReasonCancelled}
	require.NoError(t, ok.Validate())
	bad := dto.BookingExpiredPayload{BookingID: "b", PublicRef: "r", BuyerEmail: "e", Reason: "NO"}
	require.Error(t, bad.Validate())
}

func TestBookingReviewPayload_Validate(t *testing.T) {
	require.NoError(t, dto.BookingReviewPayload{BookingID: "b", BuyerEmail: "e", AmountMinor: 0}.Validate())
	require.Error(t, dto.BookingReviewPayload{BuyerEmail: "e"}.Validate())
	require.Error(t, dto.BookingReviewPayload{BookingID: "b", BuyerEmail: "e", AmountMinor: -1}.Validate())
}

func TestTurnPayloads_Validate(t *testing.T) {
	require.NoError(t, dto.TurnSoonPayload{EventID: "e", QueueEntryID: "q", VisitorID: "v"}.Validate())
	require.Error(t, dto.TurnSoonPayload{}.Validate())
	require.NoError(t, dto.TurnArrivedPayload{EventID: "e", QueueEntryID: "q", VisitorID: "v", ClaimDeadline: "2026-10-20T04:03:00Z"}.Validate())
	require.Error(t, dto.TurnArrivedPayload{EventID: "e"}.Validate())
}

func TestTurnArrivedPayload_ParseDeadline(t *testing.T) {
	p := dto.TurnArrivedPayload{ClaimDeadline: "2026-10-20T04:03:00Z"}
	d, err := p.ParseClaimDeadline()
	require.NoError(t, err)
	assert.Equal(t, 2026, d.Year())
	_, err = dto.TurnArrivedPayload{ClaimDeadline: "x"}.ParseClaimDeadline()
	require.Error(t, err)
}

func TestOTPPayload_DecodeAndParse(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)
	p := dto.OTPRequestedPayload{
		EmailEnc:  base64.RawURLEncoding.EncodeToString([]byte("email")),
		CodeEnc:   base64.RawURLEncoding.EncodeToString([]byte("code")),
		ExpiresAt: now.Format(time.RFC3339),
	}
	require.NoError(t, p.Validate())

	e, err := p.DecodeEmailEnc()
	require.NoError(t, err)
	assert.Equal(t, []byte("email"), e)

	c, err := p.DecodeCodeEnc()
	require.NoError(t, err)
	assert.Equal(t, []byte("code"), c)

	exp, err := p.ParseExpiresAt()
	require.NoError(t, err)
	assert.True(t, now.Equal(exp))

	assert.Equal(t, "id", p.LocaleOrDefault())
	assert.Equal(t, "en", dto.OTPRequestedPayload{Locale: "en"}.LocaleOrDefault())
	require.Error(t, dto.OTPRequestedPayload{}.Validate())
}

func TestSubscribeRequest_Validate(t *testing.T) {
	assert.Nil(t, dto.SubscribeRequest{EventID: "e", Platform: "ANDROID", PushToken: "t"}.Validate())
	d := dto.SubscribeRequest{Platform: "WEB"}.Validate()
	require.NotNil(t, d)
	assert.Contains(t, d, "eventId")
	assert.Contains(t, d, "platform")
	assert.Contains(t, d, "pushToken")
}
