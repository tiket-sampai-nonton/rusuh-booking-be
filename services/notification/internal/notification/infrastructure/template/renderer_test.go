package template_test

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/notification/internal/notification/domain/dto"
	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/notification/internal/notification/infrastructure/template"
)

func TestRenderer_Eticket(t *testing.T) {
	r, err := template.New()
	require.NoError(t, err)

	out, err := r.Render("eticket", dto.EticketData{
		PublicRef: "RBT-ABC", EventName: "Konser ABC", EventStartsAt: "2026-11-15", EventVenue: "GBK",
		Tickets:         []dto.TicketInfo{{TicketCode: "TKT-001", AttendeeName: "Budi", AttendeeNikMasked: "3174********0001", CategoryCode: "VIP"}},
		TicketLinkToken: "tok",
	})
	require.NoError(t, err)
	assert.Contains(t, out, "RBT-ABC")
	assert.Contains(t, out, "TKT-001")
	assert.Contains(t, out, "Budi")
	assert.Contains(t, out, "3174********0001") // TC-TKT-02: masked NIK present
	assert.Contains(t, out, "Konser ABC")
}

func TestRenderer_Expired(t *testing.T) {
	r, _ := template.New()
	out, err := r.Render("expired", dto.ExpiredData{PublicRef: "RBT-1", Reason: "PAYMENT_TIMEOUT"})
	require.NoError(t, err)
	assert.Contains(t, out, "RBT-1")
	assert.Contains(t, out, "PAYMENT_TIMEOUT")
}

func TestRenderer_Review_FormatsIDR(t *testing.T) {
	r, _ := template.New()
	out, err := r.Render("review", dto.ReviewData{BookingID: "b1", AmountMinor: 3000000, AttemptHint: "late"})
	require.NoError(t, err)
	assert.Contains(t, out, "b1")
	assert.Contains(t, out, "30.000") // 3000000 minor → Rp 30.000
	assert.Contains(t, out, "late")
}

func TestRenderer_OTP_FormatsTime(t *testing.T) {
	r, _ := template.New()
	exp := time.Date(2026, 10, 20, 13, 50, 0, 0, time.UTC)
	out, err := r.Render("otp_login", dto.OTPData{Code: "123456", ExpiresAt: exp, Locale: "id"})
	require.NoError(t, err)
	assert.Contains(t, out, "123456")
	assert.Contains(t, out, "2026")
}

func TestRenderer_UnknownTemplateErrors(t *testing.T) {
	r, _ := template.New()
	_, err := r.Render("nope", nil)
	require.Error(t, err)
}
