//go:build e2e

package e2e

import (
	"fmt"
	"os"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type held struct {
	b                               *buyer
	sessionID, bookingID, publicRef string
	vh                              map[string]string
}

var serialCounter atomic.Int64

func nextSerial() int { return 1 + int((time.Now().UnixNano()/1000+serialCounter.Add(1)*7)%8000) }

// startBooking takes a fresh buyer through queue -> session -> hold for one ticket.
func startBooking(t *testing.T, eventID, categoryID string) held {
	t.Helper()
	b := &buyer{email: fmt.Sprintf("buyer-%d-%d@contoh.id", time.Now().UnixNano(), serialCounter.Add(1)), nik: nikFor(nextSerial())}
	admit(t, eventID, b)
	vh := map[string]string{"X-Visitor-Token": b.visitorToken}
	s := call(t, "POST", bookingURL+"/api/v1/booking-sessions", vh, map[string]string{"admissionToken": b.admissionToken})
	mustStatus(t, 201, s, "create session")
	sid := s.str("sessionId")
	h := call(t, "POST", bookingURL+"/api/v1/bookings", map[string]string{"X-Visitor-Token": b.visitorToken, "Idempotency-Key": "e2e-hold-" + sid[:20]}, map[string]any{
		"sessionId": sid,
		"items":     []map[string]any{{"categoryId": categoryID, "attendee": map[string]string{"nik": nikFor(nextSerial()), "fullName": "Siti Aminah"}}},
	})
	mustStatus(t, 201, h, "hold")
	return held{b: b, sessionID: sid, bookingID: h.str("bookingId"), publicRef: h.str("publicRef"), vh: vh}
}

func (h held) payStatus(t *testing.T) resp {
	return call(t, "GET", paymentURL+"/api/v1/payments/"+h.bookingID+"/status", h.vh, nil)
}

func (h held) startPayment(t *testing.T) {
	t.Helper()
	r := call(t, "POST", paymentURL+"/api/v1/payments/"+h.bookingID+"/attempts",
		map[string]string{"X-Visitor-Token": h.b.visitorToken, "Idempotency-Key": "e2e-attempt-" + h.sessionID[:20]}, map[string]string{"method": "QRIS"})
	mustStatus(t, 201, r, "create attempt")
}

func webhook(t *testing.T, n map[string]any) resp {
	return call(t, "POST", paymentURL+"/api/v1/webhooks/midtrans", nil, n)
}

func attemptStatus(r resp) string {
	a, _ := r.Data["attempt"].(map[string]any)
	s, _ := a["status"].(string)
	return s
}

func noTicketsAndNoEmail(t *testing.T, h held) {
	t.Helper()
	assert.NotEqual(t, 200, call(t, "GET", bookingURL+"/api/v1/bookings/"+h.bookingID+"/tickets", h.vh, nil).Status, "tiket tidak boleh terbit")
	time.Sleep(3 * time.Second) // give the notification worker time; nothing must arrive
	for _, m := range mailsTo(t, h.b.email) {
		assert.NotContains(t, m.Subject, "Tiket", "tidak ada e-tiket: %s", m.Subject)
	}
}

func TestPayment_PendingThenFailedNeverPaysOrIssuesTickets(t *testing.T) {
	key := serverKey(t)
	admin := login(t, fmt.Sprintf("admin-%d@contoh.id", time.Now().UnixNano()))
	eventID, categoryID := newEvent(t, admin, 10)
	h := startBooking(t, eventID, categoryID)
	h.startPayment(t)
	orderID := h.publicRef + "-1"

	mustStatus(t, 200, webhook(t, notification(orderID, "pending", "150000.00", key)), "pending webhook")
	st := h.payStatus(t)
	assert.NotEqual(t, "PAID", st.str("bookingStatus"))
	assert.Equal(t, "PENDING", attemptStatus(st))

	mustStatus(t, 200, webhook(t, notification(orderID, "deny", "150000.00", key)), "deny webhook")
	eventually(t, 10*time.Second, "attempt gagal", func() bool { return attemptStatus(h.payStatus(t)) == "FAILED" })
	assert.NotEqual(t, "PAID", h.payStatus(t).str("bookingStatus"))
	noTicketsAndNoEmail(t, h)

	// A late "settlement" for a failed attempt must not pay the booking either.
	webhook(t, notification(orderID, "settlement", "150000.00", key))
	time.Sleep(2 * time.Second)
	assert.NotEqual(t, "PAID", h.payStatus(t).str("bookingStatus"), "attempt yang sudah gagal tidak boleh hidup lagi")
}

func TestHold_DuplicateRequestsAndUnauthorizedAccess(t *testing.T) {
	admin := login(t, fmt.Sprintf("admin-%d@contoh.id", time.Now().UnixNano()))
	eventID, categoryID := newEvent(t, admin, 10)
	h := startBooking(t, eventID, categoryID)

	// Same Idempotency-Key and body: the same booking comes back, no second hold.
	again := call(t, "POST", bookingURL+"/api/v1/bookings", map[string]string{"X-Visitor-Token": h.b.visitorToken, "Idempotency-Key": "e2e-hold-" + h.sessionID[:20]}, map[string]any{
		"sessionId": h.sessionID,
		"items":     []map[string]any{{"categoryId": categoryID, "attendee": map[string]string{"nik": nikFor(nextSerial()), "fullName": "Siti Aminah"}}},
	})
	assert.NotEqual(t, 500, again.Status, again.Raw)
	if again.Status == 201 {
		assert.Equal(t, h.bookingID, again.str("bookingId"), "replay tidak boleh membuat booking baru")
	}

	// Another visitor must not read, cancel, or pay for this booking; unauthenticated callers neither.
	other := &buyer{email: fmt.Sprintf("other-%d@contoh.id", time.Now().UnixNano()), nik: nikFor(nextSerial())}
	admit(t, eventID, other)
	oh := map[string]string{"X-Visitor-Token": other.visitorToken}
	for name, hdr := range map[string]map[string]string{"visitor lain": oh, "tanpa token": nil, "token palsu": {"X-Visitor-Token": "bukan-token"}, "X-Visitor-ID dipalsukan": {"X-Visitor-ID": h.sessionID}} {
		r := call(t, "GET", bookingURL+"/api/v1/bookings/"+h.bookingID, hdr, nil)
		assert.Contains(t, []int{401, 403, 404}, r.Status, "%s: %s", name, r.Raw)
		assert.NotEqual(t, h.bookingID, r.str("bookingId"), name)
		assert.Contains(t, []int{401, 403, 404}, call(t, "POST", bookingURL+"/api/v1/bookings/"+h.bookingID+"/cancel", hdr, nil).Status, name)
		assert.Contains(t, []int{401, 403, 404, 409}, call(t, "GET", paymentURL+"/api/v1/payments/"+h.bookingID+"/status", hdr, nil).Status, name)
		assert.NotEqual(t, 200, call(t, "GET", bookingURL+"/api/v1/bookings/"+h.bookingID+"/tickets", hdr, nil).Status, name)
	}
}

// Slow (about 5 minutes: payment TTL is at least 120 s, then a 180 s verification grace). Run with E2E_SLOW=1.
func TestExpiredPayment_LateSettlementGoesToReviewWithoutTickets(t *testing.T) {
	if os.Getenv("E2E_SLOW") == "" {
		t.Skip("lambat (~5 menit); jalankan dengan E2E_SLOW=1")
	}
	key := serverKey(t)
	admin := login(t, fmt.Sprintf("admin-%d@contoh.id", time.Now().UnixNano()))
	eventID, categoryID := newEvent(t, admin, 10)
	ah := map[string]string{"Authorization": "Bearer " + admin}
	// 120 s is the shortest payment TTL (selection must be above the warning/grace seconds and not above payment); out-of-range values are a 400, not a 500.
	mustStatus(t, 400, call(t, "PATCH", bookingURL+"/internal/v1/admin/events/"+eventID, ah, map[string]any{"paymentTtlSeconds": 15}), "ttl di bawah batas")
	mustStatus(t, 200, call(t, "PATCH", bookingURL+"/internal/v1/admin/events/"+eventID, ah, map[string]any{"selectionTtlSeconds": 90, "paymentTtlSeconds": 120}), "short ttl")

	h := startBooking(t, eventID, categoryID)
	// payment TTL (120 s) + verification grace (180 s) + sweeper interval
	eventually(t, 330*time.Second, "booking EXPIRED oleh sweeper", func() bool {
		return call(t, "GET", bookingURL+"/api/v1/bookings/"+h.bookingID, h.vh, nil).str("status") == "EXPIRED"
	})
	// The FE can no longer start a payment for it.
	r := call(t, "POST", paymentURL+"/api/v1/payments/"+h.bookingID+"/attempts",
		map[string]string{"X-Visitor-Token": h.b.visitorToken, "Idempotency-Key": "e2e-late-" + h.sessionID[:20]}, map[string]string{"method": "QRIS"})
	assert.Contains(t, []int{404, 409}, r.Status, r.Raw)

	// Money arriving after the deadline needs a human: REVIEW, no tickets.
	webhook(t, notification(h.publicRef+"-1", "settlement", "150000.00", key))
	time.Sleep(3 * time.Second)
	assert.NotEqual(t, "PAID", call(t, "GET", bookingURL+"/api/v1/bookings/"+h.bookingID, h.vh, nil).str("status"))
	noTicketsAndNoEmail(t, h)
}

func TestConcurrentBuyers_NeverOversell(t *testing.T) {
	admin := login(t, fmt.Sprintf("admin-%d@contoh.id", time.Now().UnixNano()))
	const capacity, buyers = 2, 6
	eventID, categoryID := newEventWith(t, admin, capacity, 1)

	var wg sync.WaitGroup
	var holds, rejected atomic.Int64
	for i := 0; i < buyers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			b := &buyer{email: fmt.Sprintf("race-%d-%d@contoh.id", time.Now().UnixNano(), serialCounter.Add(1)), nik: nikFor(nextSerial())}
			j := call(t, "POST", queueURL+"/api/v1/events/"+eventID+"/queue-entries", map[string]string{"X-Client-Type": "mobile"}, map[string]any{
				"buyerNik": b.nik, "email": b.email, "emailConfirmation": b.email, "consentNik": true,
			})
			if j.Status != 201 {
				rejected.Add(1)
				return
			}
			b.visitorToken = j.str("visitorToken")
			// Not every join is admitted at once; the Promotor admits waiting entries as stock is allocated.
			admitted := j.str("status") == "ADMITTED"
			for i := 0; !admitted && i < 20; i++ {
				time.Sleep(500 * time.Millisecond)
				admitted = call(t, "GET", queueURL+"/api/v1/queue-entries/me?eventId="+eventID, map[string]string{"X-Visitor-Token": b.visitorToken}, nil).str("status") == "ADMITTED"
			}
			if !admitted {
				rejected.Add(1) // stock ran out: stays waiting
				t.Logf("waiting: %s", call(t, "GET", queueURL+"/api/v1/queue-entries/me?eventId="+eventID, map[string]string{"X-Visitor-Token": b.visitorToken}, nil).Raw)
				return
			}
			c := call(t, "POST", queueURL+"/api/v1/queue-entries/"+j.str("entryId")+"/claim?eventId="+eventID, map[string]string{"X-Visitor-Token": b.visitorToken}, nil)
			if c.Status != 200 {
				t.Logf("claim: %s", c.Raw)
				rejected.Add(1)
				return
			}
			s := call(t, "POST", bookingURL+"/api/v1/booking-sessions", map[string]string{"X-Visitor-Token": b.visitorToken}, map[string]string{"admissionToken": c.str("admissionToken")})
			if s.Status != 201 {
				t.Logf("session: %s", s.Raw)
				rejected.Add(1)
				return
			}
			sid := s.str("sessionId")
			h := call(t, "POST", bookingURL+"/api/v1/bookings", map[string]string{"X-Visitor-Token": b.visitorToken, "Idempotency-Key": "e2e-race-" + sid[:20]}, map[string]any{
				"sessionId": sid, "items": []map[string]any{{"categoryId": categoryID, "attendee": map[string]string{"nik": nikFor(nextSerial()), "fullName": "Peserta"}}},
			})
			if h.Status == 201 {
				holds.Add(1)
			} else {
				t.Logf("hold: %s", h.Raw)
				rejected.Add(1)
			}
		}()
	}
	wg.Wait()
	assert.LessOrEqual(t, int(holds.Load()), capacity, "tidak boleh melebihi kapasitas (holds=%d rejected=%d)", holds.Load(), rejected.Load())
	assert.Equal(t, int64(capacity), holds.Load(), "stok yang ada harus terjual habis")
	d := call(t, "GET", bookingURL+"/api/v1/events/"+eventID, nil, nil)
	cats, _ := d.Data["categories"].([]any)
	require.Len(t, cats, 1)
	assert.EqualValues(t, 0, cats[0].(map[string]any)["available"], "stok tersedia habis")
}
