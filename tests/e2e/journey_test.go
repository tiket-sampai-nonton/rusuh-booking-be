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
	eventID, categoryID = newPublishedEvent(t, adminToken, capacity, perOrder)
	configureQueue(t, adminToken, eventID, 500)
	return eventID, categoryID
}

// configureQueue sets the queue settings through the internal admin endpoint. Join is strict (QSA-6): an event is
// unjoinable until this PUT, so every journey does it after publish and before the first join.
func configureQueue(t *testing.T, adminToken, eventID string, capacity int) {
	t.Helper()
	r := call(t, "PUT", queueURL+"/internal/v1/admin/events/"+eventID+"/queue-settings",
		map[string]string{"Authorization": "Bearer " + adminToken}, map[string]any{"activeSlotCapacity": capacity})
	mustStatus(t, 201, r, "set queue settings")
}

// newPublishedEvent creates and publishes an event WITHOUT queue settings.
func newPublishedEvent(t *testing.T, adminToken string, capacity, perOrder int) (eventID, categoryID string) {
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
	// Event baru berstatus draf: tidak tampil di katalog sampai diterbitkan (SCRUM-55).
	mustStatus(t, 404, call(t, "GET", bookingURL+"/api/v1/events/"+eventID, nil, nil), "draf tidak tampil di katalog")
	mustStatus(t, 200, call(t, "POST", bookingURL+"/internal/v1/admin/events/"+eventID+"/publish", map[string]string{"Authorization": "Bearer " + adminToken}, nil), "publish event")
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

// buyerBlock is the HoldRequest.buyer block (ADR-030): the buyer's NIK and email are given at hold time, not at join.
func (b *buyer) buyerBlock() map[string]any {
	return map[string]any{"nik": b.nik, "email": b.email, "emailConfirmation": b.email, "consent": true}
}

// admit takes a buyer through the queue: anonymous join (no body, ADR-030) -> admitted -> claim -> admission token.
// X-Client-Type: mobile makes Queue return visitorToken in the body (web gets the HttpOnly rb_visitor cookie instead).
func admit(t *testing.T, eventID string, b *buyer) {
	t.Helper()
	j := call(t, "POST", queueURL+"/api/v1/events/"+eventID+"/queue-entries", map[string]string{"X-Client-Type": "mobile"}, nil)
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
		"sessionId": sessionID, "buyer": b.buyerBlock(),
		"items": []map[string]any{{"categoryId": categoryID, "attendee": map[string]string{"nik": nikFor(7001), "fullName": "Budi Santoso"}}},
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

	// Only the booking's own visitor, its account, or the email link can read the tickets - a booking id is not enough.
	stranger := &buyer{email: fmt.Sprintf("stranger-%d@contoh.id", time.Now().UnixNano()), nik: nikFor(nextSerial())}
	admit(t, eventID, stranger)
	for name, hdr := range map[string]map[string]string{
		"tanpa identitas": nil, "visitor lain": {"X-Visitor-Token": stranger.visitorToken}, "visitor palsu": {"X-Visitor-Token": "palsu"},
		"X-Visitor-ID dipalsukan": {"X-Visitor-ID": b.visitorToken},
	} {
		r := call(t, "GET", bookingURL+"/api/v1/bookings/"+bookingID+"/tickets", hdr, nil)
		assert.Contains(t, []int{401, 404}, r.Status, name+": "+r.Raw)
		assert.NotContains(t, r.Raw, code, name+": kode tiket tidak boleh bocor")
	}
	assert.Equal(t, 404, call(t, "GET", bookingURL+"/api/v1/bookings/"+bookingID+"/tickets?t=bukan-token", nil, nil).Status, "tautan palsu")

	var ticketLink string
	eventually(t, 25*time.Second, "email e-tiket", func() bool {
		for _, m := range mailsTo(t, b.email) {
			if contains(m.Text, code) {
				return true
			}
		}
		return false
	})
	n := 0
	linkRe := regexp.MustCompile(`tickets/([0-9a-f-]{36})\?t=([A-Za-z0-9_-]+)`)
	for _, m := range mailsTo(t, b.email) {
		if contains(m.Text, code) {
			n++
			if g := linkRe.FindStringSubmatch(m.Text); g != nil {
				ticketLink = g[2]
				assert.Equal(t, bookingID, g[1], "tautan menunjuk booking ini")
			}
		}
	}
	require.NotEmpty(t, ticketLink, "email e-tiket memuat tautan bertoken")
	viaLink := call(t, "GET", bookingURL+"/api/v1/bookings/"+bookingID+"/tickets?t="+ticketLink, nil, nil)
	mustStatus(t, 200, viaLink, "tiket lewat tautan email tanpa login")
	assert.Contains(t, viaLink.Raw, code)
	other := call(t, "GET", bookingURL+"/api/v1/bookings/"+hold.str("bookingId")+"x/tickets?t="+ticketLink, nil, nil)
	assert.NotEqual(t, 200, other.Status, "token tidak berlaku untuk booking lain")
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

// E2E_ADMIN_DOMAIN must match the ADMIN_EMAILS suffix the running Booking/Queue allow-list (tools/e2e.sh: admin.e2e.test).
func adminEmail() string {
	return fmt.Sprintf("admin-%d@%s", time.Now().UnixNano(), env("E2E_ADMIN_DOMAIN", "admin.e2e.test"))
}

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

// SCRUM-53: after logout the same access token stops working at once, not after its exp.
func TestLogout_RevokesTheAccessTokenAtOnce(t *testing.T) {
	email := fmt.Sprintf("logout-%d@contoh.id", time.Now().UnixNano())
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
	access, refresh := r.str("accessToken"), r.str("refreshToken")
	require.NotEmpty(t, access)
	require.NotEmpty(t, refresh)
	bearer := map[string]string{"Authorization": "Bearer " + access}

	mustStatus(t, 200, call(t, "GET", userURL+"/api/v1/me", bearer, nil), "me sebelum logout")
	mustStatus(t, 204, call(t, "POST", userURL+"/api/v1/auth/logout", nil, map[string]string{"refreshToken": refresh}), "logout")
	assert.Equal(t, 401, call(t, "GET", userURL+"/api/v1/me", bearer, nil).Status, "access token yang sama ditolak setelah logout")
}

// SCRUM-56: a cancelled event leaves the default catalog list but stays readable, and no new hold can start.
func TestCancelledEvent_LeavesTheListButStaysReadable(t *testing.T) {
	admin := adminToken(t)
	eventID, _ := newEvent(t, admin, 5)
	hdr := map[string]string{"Authorization": "Bearer " + admin}

	mustStatus(t, 200, call(t, "POST", bookingURL+"/internal/v1/admin/events/"+eventID+"/cancel", hdr, nil), "cancel event")
	d := call(t, "GET", bookingURL+"/api/v1/events/"+eventID, nil, nil)
	mustStatus(t, 200, d, "detail tetap terbuka")
	ev, _ := d.Data["event"].(map[string]any)
	assert.Equal(t, "CANCELLED", ev["status"])

	list := call(t, "GET", bookingURL+"/api/v1/events", nil, nil)
	mustStatus(t, 200, list, "daftar katalog")
	assert.NotContains(t, list.Raw, eventID, "event dibatalkan tidak ada di daftar bawaan")
	assert.Contains(t, call(t, "GET", bookingURL+"/api/v1/events?status=CANCELLED", nil, nil).Raw, eventID, "bisa disaring lewat status")
}

// QSA-6 / QSA-1 / QSA-2 end to end: an event without queue settings is unjoinable; the internal admin endpoint
// configures it (201, then 200 on a repeat); only an allow-listed admin may use it; afterwards the join works.
func TestQueueSettings_StrictJoinAndAdminEndpoint(t *testing.T) {
	admin := adminToken(t)
	eventID, _ := newPublishedEvent(t, admin, 5, 1)
	path := queueURL + "/internal/v1/admin/events/" + eventID + "/queue-settings"
	adminH := map[string]string{"Authorization": "Bearer " + admin}
	join := func() resp {
		return call(t, "POST", queueURL+"/api/v1/events/"+eventID+"/queue-entries", map[string]string{"X-Client-Type": "mobile"}, nil)
	}

	r := join()
	assert.Equal(t, 409, r.Status, r.Raw)
	assert.Contains(t, r.Raw, "QUEUE_NOT_CONFIGURED")
	assert.Equal(t, 404, call(t, "GET", path, adminH, nil).Status, "GET sebelum dikonfigurasi")

	assert.Equal(t, 401, call(t, "PUT", path, nil, map[string]any{"activeSlotCapacity": 5}).Status, "tanpa token")
	buyerToken := login(t, fmt.Sprintf("buyer-%d@contoh.id", time.Now().UnixNano()))
	assert.Equal(t, 401, call(t, "PUT", path, map[string]string{"Authorization": "Bearer " + buyerToken}, map[string]any{"activeSlotCapacity": 5}).Status, "pembeli bukan admin")
	assert.Equal(t, 400, call(t, "PUT", path, adminH, map[string]any{"activeSlotCapacity": 0}).Status, "di luar batas")

	mustStatus(t, 201, call(t, "PUT", path, adminH, map[string]any{"activeSlotCapacity": 5}), "buat pengaturan")
	mustStatus(t, 200, call(t, "PUT", path, adminH, map[string]any{"activeSlotCapacity": 5}), "ulangi tanpa perubahan")
	g := call(t, "GET", path, adminH, nil)
	mustStatus(t, 200, g, "baca pengaturan")
	assert.Equal(t, "5", fmt.Sprint(g.Data["activeSlotCapacity"]), g.Raw)

	mustStatus(t, 201, join(), "join setelah dikonfigurasi")
}
