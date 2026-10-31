//go:build e2e

package steps

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/cucumber/godog"
	"github.com/tiket-sampai-nonton/rusuh-booking-be/tests/e2e/helpers"
)

type cancelCtx struct {
	env       *helpers.Env
	eventID   string
	catID     string
	admit     helpers.AdmitResult
	bookingID string
	resp      *http.Response
	body      map[string]any
}

// RegisterCancel registers cancel, expiry and AMQP settlement step definitions.
func RegisterCancel(sc *godog.ScenarioContext, env *helpers.Env) {
	c := &cancelCtx{env: env}

	sc.After(func(ctx context.Context, _ *godog.Scenario, _ error) (context.Context, error) {
		if c.eventID != "" {
			_ = helpers.CleanupEvent(ctx, env.Pool, c.eventID)
			c.eventID = ""
		}
		c.admit, c.bookingID = helpers.AdmitResult{}, ""
		c.resp, c.body = nil, nil
		return ctx, nil
	})

	sc.Step(`^a seeded cancel event with (\d+) seats$`, c.seedEvent)
	sc.Step(`^a cancel visitor holds (\d+) ticket$`, c.holdOne)
	sc.Step(`^a payment-lock is applied on the cancel booking$`, c.applyLock)
	sc.Step(`^the cancel booking TTL is forced to expire$`, c.forceTTL)
	sc.Step(`^the cancel booking is expired by the sweeper$`, c.expireBySweeper)
	sc.Step(`^the cancel visitor cancels the booking$`, c.cancelBooking)
	sc.Step(`^the expiry sweeper processes the cancel booking within (\d+) seconds$`, c.waitExpiry)
	sc.Step(`^payment\.succeeded is injected via AMQP for the cancel booking$`, c.injectPaymentSucceeded)
	sc.Step(`^the consumer processes it within (\d+) seconds$`, c.waitForReview)
	sc.Step(`^the cancel response status is (\d+)$`, c.cancelStatusIs)
	sc.Step(`^the cancel booking status is "([^"]*)"$`, c.cancelBookingStatusIs)
	sc.Step(`^cancel held units return to 0$`, c.heldUnitsZero)
	sc.Step(`^the cancel error code is "([^"]*)"$`, c.cancelErrorCode)
	sc.Step(`^no tickets were issued for the cancel booking$`, c.noTickets)
	sc.Step(`^cancel outbox contains "([^"]*)"$`, c.outboxContains)
}

func (c *cancelCtx) seedEvent(cap int) error {
	eID, catID, err := helpers.SeedEvent(context.Background(), c.env.Pool, cap)
	c.eventID = eID
	c.catID = catID
	return err
}

func (c *cancelCtx) holdOne(_ int) error {
	admit, err := helpers.AdmitVisitor(c.env, c.eventID, "3174010101800001", "cancel@example.test")
	if err != nil {
		return err
	}
	c.admit = admit
	sessionID, err := helpers.CreateSession(c.env, admit.AdmissionToken, admit.VisitorToken)
	if err != nil {
		return err
	}
	r := helpers.HoldTickets(c.env, sessionID, admit.VisitorToken,
		fmt.Sprintf("cancel-key-%d", time.Now().UnixNano()),
		[]map[string]any{{"categoryId": c.catID, "attendee": map[string]string{"nik": "3174010101800001", "fullName": "Budi"}}})
	if r.StatusCode != 201 {
		raw, _ := json.Marshal(r.Body)
		return fmt.Errorf("hold failed: %d %s", r.StatusCode, raw)
	}
	c.bookingID = r.BookingID
	return nil
}

func (c *cancelCtx) applyLock() error {
	resp, err := helpers.PostJSON(context.Background(),
		helpers.Booking(c.env, "/internal/v1/bookings/"+c.bookingID+"/payment-lock"),
		map[string]any{"attemptKey": fmt.Sprintf("lock-%d", time.Now().UnixNano())},
		map[string]string{"Authorization": "Bearer " + c.env.ServiceToken})
	if err != nil {
		return err
	}
	if resp.StatusCode != 200 {
		return fmt.Errorf("payment-lock failed: %d", resp.StatusCode)
	}
	return nil
}

func (c *cancelCtx) forceTTL() error {
	return helpers.ForceExpireBooking(context.Background(), c.env.Pool, c.bookingID)
}

func (c *cancelCtx) expireBySweeper() error {
	if err := c.forceTTL(); err != nil {
		return err
	}
	return helpers.WaitForBookingStatus(context.Background(), c.env.Pool, c.bookingID, "EXPIRED", 30)
}

func (c *cancelCtx) cancelBooking() error {
	resp, err := helpers.PostJSON(context.Background(),
		helpers.Booking(c.env, "/api/v1/bookings/"+c.bookingID+"/cancel"),
		nil,
		map[string]string{"X-Visitor-Token": c.admit.VisitorToken})
	if err != nil {
		return err
	}
	c.resp = resp
	var body map[string]any
	_ = helpers.DecodeBody(resp, &body)
	c.body = body
	return nil
}

func (c *cancelCtx) waitExpiry(secs int) error {
	if err := c.forceTTL(); err != nil {
		return err
	}
	return helpers.WaitForBookingStatus(context.Background(), c.env.Pool, c.bookingID, "EXPIRED", secs)
}

func (c *cancelCtx) injectPaymentSucceeded() error {
	return helpers.InjectPaymentSucceeded(c.env.AMQPURL, c.bookingID, "VA_BCA", time.Now())
}

func (c *cancelCtx) waitForReview(secs int) error {
	return helpers.WaitForBookingStatus(context.Background(), c.env.Pool, c.bookingID, "REVIEW", secs)
}

func (c *cancelCtx) cancelStatusIs(expected int) error {
	if c.resp == nil {
		return fmt.Errorf("no response recorded")
	}
	if c.resp.StatusCode != expected {
		raw, _ := json.Marshal(c.body)
		return fmt.Errorf("expected %d, got %d: %s", expected, c.resp.StatusCode, raw)
	}
	return nil
}

func (c *cancelCtx) cancelBookingStatusIs(expected string) error {
	status, err := helpers.BookingStatus(context.Background(), c.env.Pool, c.bookingID)
	if err != nil {
		return err
	}
	if status != expected {
		return fmt.Errorf("expected booking status %q, got %q", expected, status)
	}
	return nil
}

func (c *cancelCtx) heldUnitsZero() error {
	held, err := helpers.HeldUnits(context.Background(), c.env.Pool, c.eventID)
	if err != nil {
		return err
	}
	if held != 0 {
		return fmt.Errorf("expected held_units=0, got %d", held)
	}
	return nil
}

func (c *cancelCtx) cancelErrorCode(code string) error {
	got := helpers.ErrorCode(c.body)
	if got != code {
		raw, _ := json.Marshal(c.body)
		return fmt.Errorf("expected error code %q, got %q (body: %s)", code, got, raw)
	}
	return nil
}

func (c *cancelCtx) noTickets() error {
	n, err := helpers.TicketCount(context.Background(), c.env.Pool, c.bookingID)
	if err != nil {
		return err
	}
	if n != 0 {
		return fmt.Errorf("expected 0 tickets, got %d", n)
	}
	return nil
}

func (c *cancelCtx) outboxContains(routingKey string) error {
	n, err := helpers.OutboxCount(context.Background(), c.env.Pool, routingKey, c.bookingID)
	if err != nil {
		return err
	}
	if n == 0 {
		return fmt.Errorf("no outbox row with routing_key=%q for booking %s", routingKey, c.bookingID)
	}
	return nil
}
