//go:build e2e

package e2e

import (
	"fmt"
	"regexp"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

var otpRe = regexp.MustCompile(`\b(\d{6})\b`)

// login runs the OTP login (User -> RabbitMQ -> Notification -> Mailpit -> User) and returns the access token.
func login(t *testing.T, email string) string {
	t.Helper()
	mustStatus(t, 202, call(t, "POST", userURL+"/api/v1/auth/otp/request", nil, map[string]string{"email": email}), "otp request")
	var code string
	eventually(t, 15*time.Second, "email OTP di Mailpit", func() bool {
		for _, m := range mailsTo(t, email) {
			if g := otpRe.FindStringSubmatch(m.Text); g != nil {
				code = g[1]
				return true
			}
		}
		return false
	})
	r := call(t, "POST", userURL+"/api/v1/auth/otp/verify", nil, map[string]string{"email": email, "code": code})
	mustStatus(t, 200, r, "otp verify")
	tok := r.str("accessToken")
	require.NotEmpty(t, tok, r.Raw)
	return tok
}

// newEvent creates an event with one category through the admin endpoint and returns eventId and categoryId.
func newEvent(t *testing.T, adminToken string, capacity int) (eventID, categoryID string) {
	t.Helper()
	return newEventWith(t, adminToken, capacity, 3)
}

// newEventWith sets maxTicketsPerOrder; an admission reserves that many units as the buyer's guarantee, so a small
// stock needs a small per-order limit to admit anyone at all.
func newEventWith(t *testing.T, adminToken string, capacity, perOrder int) (eventID, categoryID string) {
	t.Helper()
	slug := fmt.Sprintf("e2e-%d", time.Now().UnixNano())
	r := call(t, "POST", bookingURL+"/internal/v1/admin/events", map[string]string{"Authorization": "Bearer " + adminToken}, map[string]any{
		"name": "E2E Konser", "slug": slug, "venue": "GBK",
		"startsAt":           time.Now().Add(72 * time.Hour).UTC().Format(time.RFC3339),
		"salesStartsAt":      time.Now().Add(-1 * time.Hour).UTC().Format(time.RFC3339),
		"salesEndsAt":        time.Now().Add(2 * time.Hour).UTC().Format(time.RFC3339),
		"maxTicketsPerOrder": perOrder, "maxTicketsPerBuyerNik": 3, "allowMixedCategories": true,
		"categories": []map[string]any{{"code": "VIP", "name": "VIP", "priceMinor": 150000, "capacity": capacity}},
	})
	mustStatus(t, 201, r, "create event")
	eventID = r.str("eventId")
	d := call(t, "GET", bookingURL+"/api/v1/events/"+eventID, nil, nil)
	mustStatus(t, 200, d, "event detail")
	cats, _ := d.Data["categories"].([]any)
	require.Len(t, cats, 1, d.Raw)
	categoryID, _ = cats[0].(map[string]any)["categoryId"].(string)
	require.NotEmpty(t, categoryID)
	return eventID, categoryID
}

type buyer struct {
	email, nik, visitorToken, admissionToken string
}

// admit takes a buyer through the queue: join -> admitted -> claim -> admission token.
func admit(t *testing.T, eventID string, b *buyer) {
	t.Helper()
	j := call(t, "POST", queueURL+"/api/v1/events/"+eventID+"/queue-entries", map[string]string{"X-Client-Type": "mobile"}, map[string]any{
		"buyerNik": b.nik, "email": b.email, "emailConfirmation": b.email, "consentNik": true,
	})
	mustStatus(t, 201, j, "join queue")
	entryID := j.str("entryId")
	b.visitorToken = j.str("visitorToken")
	require.NotEmpty(t, entryID, j.Raw)
	require.NotEmpty(t, b.visitorToken, "visitorToken (mobile) kosong: "+j.Raw)
	h := map[string]string{"X-Visitor-Token": b.visitorToken}
	eventually(t, 30*time.Second, "giliran tiba (ADMITTED)", func() bool {
		r := call(t, "GET", queueURL+"/api/v1/queue-entries/me?eventId="+eventID, h, nil)
		return r.Status == 200 && r.str("status") == "ADMITTED"
	})
	c := call(t, "POST", queueURL+"/api/v1/queue-entries/"+entryID+"/claim?eventId="+eventID, h, nil)
	mustStatus(t, 200, c, "claim")
	b.admissionToken = c.str("admissionToken")
	require.NotEmpty(t, b.admissionToken, c.Raw)
}

func TestJourney_LoginToPaidTicketAndEmail(t *testing.T) {
	key := serverKey(t)
	admin := adminToken(t)
	eventID, categoryID := newEvent(t, admin, 10)

	b := &buyer{email: fmt.Sprintf("buyer-%d@contoh.id", time.Now().UnixNano()), nik: nikFor(1 + int(time.Now().UnixNano()%8000))}
	admit(t, eventID, b)

	vh := map[string]string{"X-Visitor-Token": b.visitorToken}
	s := call(t, "POST", bookingURL+"/api/v1/booking-sessions", vh, map[string]string{"admissionToken": b.admissionToken})
	mustStatus(t, 201, s, "create session")
	sessionID := s.str("sessionId")
	require.NotEmpty(t, sessionID, s.Raw)

	hold := call(t, "POST", bookingURL+"/api/v1/bookings", map[string]string{"X-Visitor-Token": b.visitorToken, "Idempotency-Key": "e2e-hold-" + sessionID[:20]}, map[string]any{
		"sessionId": sessionID,
		"items":     []map[string]any{{"categoryId": categoryID, "attendee": map[string]string{"nik": nikFor(7001), "fullName": "Budi Santoso"}}},
	})
	mustStatus(t, 201, hold, "hold")
	bookingID, publicRef := hold.str("bookingId"), hold.str("publicRef")
	assert.Equal(t, "PENDING_PAYMENT", hold.str("status"))
	assert.Equal(t, publicRef, hold.str("orderId"), "orderId = publicRef (kontrak FE)")

	// Payment page and attempt (real Midtrans Sandbox Snap token).
	page := call(t, "GET", paymentURL+"/api/v1/payments/"+bookingID+"/page", vh, nil)
	mustStatus(t, 200, page, "payment page")
	assert.EqualValues(t, 150000, page.Data["amountMinor"])

	ah := map[string]string{"X-Visitor-Token": b.visitorToken, "Idempotency-Key": "e2e-attempt-" + sessionID[:20]}
	att := call(t, "POST", paymentURL+"/api/v1/payments/"+bookingID+"/attempts", ah, map[string]string{"method": "VA_BCA"})
	mustStatus(t, 201, att, "create attempt")
	assert.Equal(t, "PENDING", att.str("status"))
	assert.Contains(t, att.str("redirectUrl"), "midtrans.com")
	assert.NotEmpty(t, att.str("successReturnUrl"))
	again := call(t, "POST", paymentURL+"/api/v1/payments/"+bookingID+"/attempts", ah, map[string]string{"method": "VA_BCA"})
	mustStatus(t, 201, again, "idempotent replay")
	assert.Equal(t, att.str("redirectUrl"), again.str("redirectUrl"), "key yang sama tidak membuat transaksi baru")

	// Before payment the FE must not see PAID.
	st := call(t, "GET", paymentURL+"/api/v1/payments/"+bookingID+"/status", vh, nil)
	mustStatus(t, 200, st, "status before payment")
	assert.NotEqual(t, "PAID", st.str("bookingStatus"))
	assert.Equal(t, publicRef, st.str("orderId"))
	tk := call(t, "GET", bookingURL+"/api/v1/bookings/"+bookingID+"/tickets", vh, nil)
	assert.NotEqual(t, 200, tk.Status, "tiket tidak boleh terbit sebelum PAID")

	// Webhooks: forged signature rejected; a settled one (signed like Midtrans does) pays the booking.
	orderID := publicRef + "-1"
	bad := notification(orderID, "settlement", "150000.00", key)
	bad["signature_key"] = "00"
	assert.Equal(t, 401, call(t, "POST", paymentURL+"/api/v1/webhooks/midtrans", nil, bad).Status, "signature palsu")
	// Wrong amount: acknowledged (Midtrans must not retry forever) but it must not change anything; checked below.
	call(t, "POST", paymentURL+"/api/v1/webhooks/midtrans", nil, notification(orderID, "settlement", "1.00", key))
	mustStatus(t, 200, call(t, "GET", paymentURL+"/api/v1/payments/"+bookingID+"/status", vh, nil), "still readable")
	assert.NotEqual(t, "PAID", call(t, "GET", paymentURL+"/api/v1/payments/"+bookingID+"/status", vh, nil).str("bookingStatus"), "webhook tidak sah tidak boleh membayar")

	good := notification(orderID, "settlement", "150000.00", key)
	mustStatus(t, 200, call(t, "POST", paymentURL+"/api/v1/webhooks/midtrans", nil, good), "settlement webhook")
	eventually(t, 20*time.Second, "booking PAID", func() bool {
		return call(t, "GET", paymentURL+"/api/v1/payments/"+bookingID+"/status", vh, nil).str("bookingStatus") == "PAID"
	})
	mustStatus(t, 200, call(t, "POST", paymentURL+"/api/v1/webhooks/midtrans", nil, good), "duplicate webhook is acknowledged")

	// Ticket + e-ticket email.
	tickets := call(t, "GET", bookingURL+"/api/v1/bookings/"+bookingID+"/tickets", vh, nil)
	mustStatus(t, 200, tickets, "tickets")
	list, _ := tickets.Data["tickets"].([]any)
	require.Len(t, list, 1, tickets.Raw)
	first, _ := list[0].(map[string]any)
	code, _ := first["ticketCode"].(string)
	qr, _ := first["qrPayload"].(string)
	assert.Equal(t, "RBT:"+code, qr)
	assert.NotContains(t, qr, b.nik, "QR tanpa PII")
	assert.Equal(t, publicRef, tickets.str("orderId"))

	eventually(t, 25*time.Second, "email e-tiket", func() bool {
		for _, m := range mailsTo(t, b.email) {
			if contains(m.Text, code) {
				return true
			}
		}
		return false
	})
	n := 0
	for _, m := range mailsTo(t, b.email) {
		if contains(m.Text, code) {
			n++
		}
	}
	assert.Equal(t, 1, n, "webhook duplikat tidak mengirim e-tiket dua kali")
}

func contains(s, sub string) bool {
	return len(sub) > 0 && regexp.MustCompile(regexp.QuoteMeta(sub)).MatchString(s)
}

// adminEmail is an address in the domain the local Booking allow-lists (ADMIN_EMAILS=@admin.e2e.test); a buyer
// token of any other address must not reach the admin endpoints.
var (
	adminOnce sync.Once
	adminTok  string
)

// adminToken logs in once per test run (OTP requests are limited to a few per hour per IP); the 15 minute access token
// outlives the whole suite.
func adminToken(t *testing.T) string {
	t.Helper()
	adminOnce.Do(func() { adminTok = login(t, adminEmail()) })
	require.NotEmpty(t, adminTok)
	return adminTok
}

func adminEmail() string { return fmt.Sprintf("admin-%d@admin.e2e.test", time.Now().UnixNano()) }

func TestAdmin_OnlyAllowListedAccountsCanCreateEvents(t *testing.T) {
	body := map[string]any{
		"name": "x", "slug": fmt.Sprintf("probe-%d", time.Now().UnixNano()), "venue": "v",
		"startsAt": time.Now().Add(72 * time.Hour).UTC().Format(time.RFC3339), "salesStartsAt": time.Now().Add(-time.Hour).UTC().Format(time.RFC3339),
		"salesEndsAt": time.Now().Add(2 * time.Hour).UTC().Format(time.RFC3339), "maxTicketsPerOrder": 3, "maxTicketsPerBuyerNik": 3,
		"categories": []map[string]any{{"code": "A", "name": "A", "priceMinor": 1000, "capacity": 1}},
	}
	// No token, a visitor token (public: obtained by joining any queue), and a buyer token of a non-admin all fail.
	admin := adminToken(t)
	eventID, _ := newEvent(t, admin, 1)
	b := &buyer{email: fmt.Sprintf("v-%d@contoh.id", time.Now().UnixNano()), nik: nikFor(nextSerial())}
	admit(t, eventID, b)
	buyerToken := login(t, fmt.Sprintf("buyer-%d@contoh.id", time.Now().UnixNano()))
	for name, hdr := range map[string]map[string]string{
		"tanpa token":            nil,
		"token visitor":          {"Authorization": "Bearer " + b.visitorToken},
		"token admission":        {"Authorization": "Bearer " + b.admissionToken},
		"pembeli bukan admin":    {"Authorization": "Bearer " + buyerToken},
		"internal pakai visitor": nil,
	} {
		if name == "internal pakai visitor" {
			r := call(t, "GET", bookingURL+"/internal/v1/events/"+eventID+"/supply", map[string]string{"Authorization": "Bearer " + b.visitorToken}, nil)
			assert.Equal(t, 401, r.Status, name+": "+r.Raw)
			continue
		}
		assert.Equal(t, 401, call(t, "POST", bookingURL+"/internal/v1/admin/events", hdr, body).Status, name)
	}
	ok := call(t, "POST", bookingURL+"/internal/v1/admin/events", map[string]string{"Authorization": "Bearer " + admin}, body)
	assert.Equal(t, 201, ok.Status, "admin sah: "+ok.Raw)
}
