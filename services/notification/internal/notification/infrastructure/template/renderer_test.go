package template_test

import (
	"encoding/json"
	"fmt"
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

// The start time is shown in WIB (the payload has no zone); an unparsable value is shown as is.
func TestRenderer_EticketStartsAtInWIB(t *testing.T) {
	r, err := template.New()
	require.NoError(t, err)
	data := dto.EticketData{PublicRef: "RB-1", EventName: "Konser", EventStartsAt: "2026-12-25T19:00:00Z", EventVenue: "GBK"}
	out, err := r.Render("eticket", data)
	require.NoError(t, err)
	assert.Contains(t, out, "26 Des 2026, 02.00 WIB")

	data.EventStartsAt = "2026-11-15"
	out, err = r.Render("eticket", data)
	require.NoError(t, err)
	assert.Contains(t, out, "2026-11-15")
}

// The HTML variant carries the same facts and escapes buyer-supplied values.
func TestRenderer_EticketHTML(t *testing.T) {
	r, err := template.New()
	require.NoError(t, err)
	out, err := r.RenderHTML("eticket", dto.EticketData{
		PublicRef: "RB-1", EventName: "Konser <ABC>", EventStartsAt: "2026-12-25T19:00:00Z", EventVenue: "GBK",
		Tickets: []dto.TicketInfo{{TicketCode: "RB-TESTRSND", AttendeeName: `<script>alert(1)</script>`, AttendeeNikMasked: "3174********0001", CategoryCode: "VIP"}},
	})
	require.NoError(t, err)
	assert.Contains(t, out, "RB-TESTRSND")
	assert.Contains(t, out, "3174********0001")
	assert.Contains(t, out, "26 Des 2026, 02.00 WIB")
	assert.NotContains(t, out, "<script>")
	assert.Contains(t, out, "&lt;script&gt;")
	assert.NotContains(t, out, "Lihat e-tiket", "tanpa token tidak ada baris tautan")
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
	assert.Contains(t, out, "3.000.000") // 3000000 minor = Rp 3.000.000 (1 minor unit = 1 rupiah, sama dengan Booking)
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

// contractPaidPayload mengikuti docs/contracts/events/booking.paid.v1.schema.json (TC-TKT-02).
const contractPaidPayload = `{
  "bookingId": "11111111-1111-1111-1111-111111111111",
  "publicRef": "RB-7K2M9Q",
  "buyerEmail": "pembeli@contoh.id",
  "event": {"eventId": "22222222-2222-2222-2222-222222222222", "name": "Konser A", "startsAt": "2026-12-25T19:00:00Z", "venue": "GBK"},
  "tickets": [
    {"ticketCode": "TK-AAAA1111", "attendeeName": "Budi Santoso", "attendeeNikMasked": "3174********0001", "categoryCode": "VIP"},
    {"ticketCode": "TK-BBBB2222", "attendeeName": "Sari Dewi", "attendeeNikMasked": "3174********0002", "categoryCode": "VIP"}
  ]%s
}`

func renderPaid(t *testing.T, tokenField string) string {
	t.Helper()
	var p dto.BookingPaidPayload
	require.NoError(t, json.Unmarshal([]byte(fmt.Sprintf(contractPaidPayload, tokenField)), &p))
	require.NoError(t, p.Validate())

	r, err := template.New()
	require.NoError(t, err)
	out, err := r.Render("eticket", dto.EticketData{
		PublicRef: p.PublicRef, EventName: p.Event.Name, EventStartsAt: p.Event.StartsAt, EventVenue: p.Event.Venue,
		Tickets: p.Tickets, TicketLinkToken: p.TicketLinkToken,
	})
	require.NoError(t, err)
	return out
}

func TestRenderer_Eticket_ContractPayload(t *testing.T) {
	out := renderPaid(t, `, "ticketLinkToken": "opaque-token"`)
	for _, want := range []string{"RB-7K2M9Q", "Konser A", "GBK", "TK-AAAA1111", "TK-BBBB2222", "Budi Santoso", "Sari Dewi", "3174********0001", "3174********0002", "Lihat e-tiket: opaque-token"} {
		assert.Contains(t, out, want)
	}
	assert.NotRegexp(t, `\d{16}`, out, "NIK lengkap tidak boleh muncul di email")
}

func TestRenderer_Eticket_WithoutLinkTokenOmitsLinkLine(t *testing.T) {
	out := renderPaid(t, "")
	assert.Contains(t, out, "TK-AAAA1111")
	assert.NotContains(t, out, "Lihat e-tiket")
}

func TestRenderer_Review_AmountMatchesBookingPrice(t *testing.T) {
	r, _ := template.New()
	// Harga VIP di contoh SDD/wireframe: priceMinor 1500000 = Rp 1.500.000.
	out, err := r.Render("review", dto.ReviewData{BookingID: "b1", AmountMinor: 1500000})
	require.NoError(t, err)
	assert.Contains(t, out, "Rp 1.500.000")
	out, err = r.Render("review", dto.ReviewData{BookingID: "b1", AmountMinor: 500})
	require.NoError(t, err)
	assert.Contains(t, out, "Rp 500\n", "jumlah kecil tidak dibulatkan menjadi 5")
}
