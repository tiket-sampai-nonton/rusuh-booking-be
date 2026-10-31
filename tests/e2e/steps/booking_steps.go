//go:build e2e

package steps

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/cucumber/godog"
	"github.com/tiket-sampai-nonton/rusuh-booking-be/tests/e2e/helpers"
)

type bookingCtx struct {
	env              *helpers.Env
	eventID          string
	catID            string
	admitA           helpers.AdmitResult
	admitB           helpers.AdmitResult
	sessionIDA       string
	sessionIDB       string
	resp             *http.Response
	body             map[string]any
	bookingID        string
	firstBookingID   string
}

// RegisterBooking registers booking and session step definitions.
func RegisterBooking(sc *godog.ScenarioContext, env *helpers.Env) {
	b := &bookingCtx{env: env}

	sc.After(func(ctx context.Context, _ *godog.Scenario, _ error) (context.Context, error) {
		if b.eventID != "" {
			_ = helpers.CleanupEvent(ctx, env.Pool, b.eventID)
			b.eventID = ""
		}
		b.admitA, b.admitB = helpers.AdmitResult{}, helpers.AdmitResult{}
		b.sessionIDA, b.sessionIDB = "", ""
		b.resp, b.body = nil, nil
		b.bookingID, b.firstBookingID = "", ""
		return ctx, nil
	})

	sc.Step(`^a seeded booking event with (\d+) seats$`, b.seedEvent)
	sc.Step(`^a booking visitor is admitted$`, b.admitVisitor)
	sc.Step(`^booking visitor A holds a ticket with attendee NIK "([^"]*)"$`, b.visitorAHolds)
	sc.Step(`^a second booking visitor is admitted$`, b.admitSecondVisitor)
	sc.Step(`^booking visitor B tries to hold with the same attendee NIK "([^"]*)"$`, b.visitorBHolds)
	sc.Step(`^the booking visitor holds (\d+) tickets? with different attendee NIKs$`, b.holdWithDiffNIKs)
	sc.Step(`^the booking visitor holds (\d+) tickets? with the same attendee NIK "([^"]*)"$`, b.holdWithSameNIK)
	sc.Step(`^the booking visitor holds (\d+) ticket with key "([^"]*)"$`, b.holdWithKey)
	sc.Step(`^the same hold is resent with key "([^"]*)"$`, b.resendHoldWithKey)
	sc.Step(`^the booking visitor tries to hold with invalid NIK "([^"]*)"$`, b.holdWithInvalidNIK)
	sc.Step(`^checking attendee NIK "([^"]*)" in the session$`, b.checkAttendeeNIK)
	sc.Step(`^the booking visitor holds (\d+) ticket$`, b.holdOneTicket)
	sc.Step(`^the hold succeeds with status (\d+)$`, b.holdSucceedsStatus)
	sc.Step(`^the booking response status is (\d+)$`, b.bookingStatusIs)
	sc.Step(`^supply shows held_units equal to (\d+)$`, b.heldUnitsIs)
	sc.Step(`^the booking ID is the same as before$`, b.bookingIDIdentical)
	sc.Step(`^the booking error code is "([^"]*)"$`, b.bookingErrorCode)
	sc.Step(`^the attendee check response status is (\d+)$`, b.checkRespStatusIs)
	sc.Step(`^the attendee check result is "([^"]*)"$`, b.checkResultIs)
	sc.Step(`^the check response does not contain the NIK "([^"]*)"$`, b.checkBodyNoNIK)
	sc.Step(`^the booking response contains paymentExpiresAt$`, b.hasPaymentExpiresAt)
	sc.Step(`^paymentExpiresAt is in the future$`, b.expiresAtFuture)
}

var bookingNIKs = []string{"3174010101800001", "3174020202800002", "3174030303800003"}

func (b *bookingCtx) seedEvent(cap int) error {
	eID, catID, err := helpers.SeedEvent(context.Background(), b.env.Pool, cap)
	b.eventID = eID
	b.catID = catID
	return err
}

func (b *bookingCtx) admitVisitor() error {
	admit, err := helpers.AdmitVisitor(b.env, b.eventID, "3174020202800002", "buyer2@example.test")
	if err != nil {
		return err
	}
	b.admitA = admit
	sessionID, err := helpers.CreateSession(b.env, admit.AdmissionToken, admit.VisitorToken)
	b.sessionIDA = sessionID
	return err
}

func (b *bookingCtx) visitorAHolds(nik string) error {
	admit, err := helpers.AdmitVisitor(b.env, b.eventID, "3174010101800001", "buyer_a@example.test")
	if err != nil {
		return err
	}
	b.admitA = admit
	sessionID, _ := helpers.CreateSession(b.env, admit.AdmissionToken, admit.VisitorToken)
	b.sessionIDA = sessionID
	r := helpers.HoldTickets(b.env, sessionID, admit.VisitorToken,
		fmt.Sprintf("a-%d", time.Now().UnixNano()),
		[]map[string]any{{"categoryId": b.catID, "attendee": map[string]string{"nik": nik, "fullName": "Budi"}}})
	if r.StatusCode != 201 {
		return fmt.Errorf("visitor A hold failed: %d %v", r.StatusCode, r.Body)
	}
	b.bookingID = r.BookingID
	return nil
}

func (b *bookingCtx) admitSecondVisitor() error {
	admit, err := helpers.AdmitVisitor(b.env, b.eventID, "3174030303800003", "buyer_b@example.test")
	if err != nil {
		return err
	}
	b.admitB = admit
	sessionID, err := helpers.CreateSession(b.env, admit.AdmissionToken, admit.VisitorToken)
	b.sessionIDB = sessionID
	return err
}

func (b *bookingCtx) visitorBHolds(nik string) error {
	r := helpers.HoldTickets(b.env, b.sessionIDB, b.admitB.VisitorToken,
		fmt.Sprintf("b-%d", time.Now().UnixNano()),
		[]map[string]any{{"categoryId": b.catID, "attendee": map[string]string{"nik": nik, "fullName": "Siti"}}})
	b.resp = &http.Response{StatusCode: r.StatusCode}
	b.body = r.Body
	return nil
}

func (b *bookingCtx) holdWithDiffNIKs(n int) error {
	items := make([]map[string]any, n)
	for i := 0; i < n; i++ {
		items[i] = map[string]any{
			"categoryId": b.catID,
			"attendee":   map[string]string{"nik": bookingNIKs[i%len(bookingNIKs)], "fullName": fmt.Sprintf("Budi %d", i+1)},
		}
	}
	r := helpers.HoldTickets(b.env, b.sessionIDA, b.admitA.VisitorToken,
		fmt.Sprintf("k-%d", time.Now().UnixNano()), items)
	b.resp = &http.Response{StatusCode: r.StatusCode}
	b.body = r.Body
	b.bookingID = r.BookingID
	return nil
}

func (b *bookingCtx) holdWithSameNIK(n int, nik string) error {
	items := make([]map[string]any, n)
	for i := 0; i < n; i++ {
		items[i] = map[string]any{
			"categoryId": b.catID,
			"attendee":   map[string]string{"nik": nik, "fullName": fmt.Sprintf("Person %d", i+1)},
		}
	}
	r := helpers.HoldTickets(b.env, b.sessionIDA, b.admitA.VisitorToken,
		fmt.Sprintf("k-%d", time.Now().UnixNano()), items)
	b.resp = &http.Response{StatusCode: r.StatusCode}
	b.body = r.Body
	return nil
}

func (b *bookingCtx) holdWithKey(_ int, key string) error {
	r := helpers.HoldTickets(b.env, b.sessionIDA, b.admitA.VisitorToken, key,
		[]map[string]any{{"categoryId": b.catID, "attendee": map[string]string{"nik": "3174010101800001", "fullName": "Budi"}}})
	b.resp = &http.Response{StatusCode: r.StatusCode}
	b.body = r.Body
	b.firstBookingID = r.BookingID
	b.bookingID = r.BookingID
	return nil
}

func (b *bookingCtx) resendHoldWithKey(key string) error {
	return b.holdWithKey(1, key)
}

func (b *bookingCtx) holdWithInvalidNIK(nik string) error {
	r := helpers.HoldTickets(b.env, b.sessionIDA, b.admitA.VisitorToken,
		fmt.Sprintf("k-%d", time.Now().UnixNano()),
		[]map[string]any{{"categoryId": b.catID, "attendee": map[string]string{"nik": nik, "fullName": "Budi"}}})
	b.resp = &http.Response{StatusCode: r.StatusCode}
	b.body = r.Body
	return nil
}

func (b *bookingCtx) checkAttendeeNIK(nik string) error {
	resp, err := helpers.PostJSON(context.Background(),
		helpers.Booking(b.env, "/api/v1/booking-sessions/"+b.sessionIDA+"/attendee-checks"),
		map[string]string{"nik": nik},
		map[string]string{"X-Visitor-Token": b.admitA.VisitorToken})
	if err != nil {
		return err
	}
	b.resp = resp
	var body map[string]any
	_ = helpers.DecodeBody(resp, &body)
	b.body = body
	return nil
}

func (b *bookingCtx) holdOneTicket() error {
	return b.holdWithDiffNIKs(1)
}

func (b *bookingCtx) holdSucceedsStatus(status int) error {
	if b.resp == nil {
		return fmt.Errorf("no response")
	}
	if b.resp.StatusCode != status {
		raw, _ := json.Marshal(b.body)
		return fmt.Errorf("expected %d, got %d: %s", status, b.resp.StatusCode, raw)
	}
	return nil
}

func (b *bookingCtx) bookingStatusIs(expected int) error {
	return b.holdSucceedsStatus(expected)
}

func (b *bookingCtx) heldUnitsIs(n int) error {
	held, err := helpers.HeldUnits(context.Background(), b.env.Pool, b.eventID)
	if err != nil {
		return err
	}
	if held != n {
		return fmt.Errorf("expected held_units=%d, got %d", n, held)
	}
	return nil
}

func (b *bookingCtx) bookingIDIdentical() error {
	if b.bookingID != b.firstBookingID {
		return fmt.Errorf("booking IDs differ: first=%s, second=%s", b.firstBookingID, b.bookingID)
	}
	return nil
}

func (b *bookingCtx) bookingErrorCode(code string) error {
	got := helpers.ErrorCode(b.body)
	if got != code {
		raw, _ := json.Marshal(b.body)
		return fmt.Errorf("expected error code %q, got %q (body: %s)", code, got, raw)
	}
	return nil
}

func (b *bookingCtx) checkRespStatusIs(expected int) error {
	return b.holdSucceedsStatus(expected)
}

func (b *bookingCtx) checkResultIs(result string) error {
	data, _ := b.body["data"].(map[string]any)
	got, _ := data["result"].(string)
	if got != result {
		return fmt.Errorf("expected check result %q, got %q", result, got)
	}
	return nil
}

func (b *bookingCtx) checkBodyNoNIK(nik string) error {
	raw, _ := json.Marshal(b.body)
	if strings.Contains(string(raw), nik) {
		return fmt.Errorf("NIK %q found in check response — PII leak", nik)
	}
	return nil
}

func (b *bookingCtx) hasPaymentExpiresAt() error {
	data, _ := b.body["data"].(map[string]any)
	v := data["paymentExpiresAt"]
	if v == nil || v == "" {
		return fmt.Errorf("paymentExpiresAt missing from booking response")
	}
	return nil
}

func (b *bookingCtx) expiresAtFuture() error {
	data, _ := b.body["data"].(map[string]any)
	s, _ := data["paymentExpiresAt"].(string)
	t, err := time.Parse(time.RFC3339, s)
	if err != nil {
		return fmt.Errorf("cannot parse paymentExpiresAt %q: %v", s, err)
	}
	if !t.After(time.Now()) {
		return fmt.Errorf("paymentExpiresAt %v is not in the future", t)
	}
	return nil
}
