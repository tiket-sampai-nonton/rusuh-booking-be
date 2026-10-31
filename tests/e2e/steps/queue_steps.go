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

type queueCtx struct {
	env              *helpers.Env
	eventID          string
	catID            string
	admit            helpers.AdmitResult
	sessionID        string
	initialAllocated int
	resp             *http.Response
	body             map[string]any
	bookingID        string
}

// RegisterQueue registers queue and admission step definitions.
func RegisterQueue(sc *godog.ScenarioContext, env *helpers.Env) {
	q := &queueCtx{env: env}

	sc.After(func(ctx context.Context, _ *godog.Scenario, _ error) (context.Context, error) {
		if q.eventID != "" {
			_ = helpers.CleanupEvent(ctx, env.Pool, q.eventID)
			q.eventID = ""
		}
		q.admit, q.sessionID, q.bookingID = helpers.AdmitResult{}, "", ""
		q.resp, q.body = nil, nil
		return ctx, nil
	})

	sc.Step(`^a seeded queue event with (\d+) seats$`, q.seedEvent)
	sc.Step(`^a queue visitor joins$`, q.aVisitorJoins)
	sc.Step(`^the visitor is ADMITTED or moves to ADMITTED within (\d+) seconds$`, q.waitAdmitted)
	sc.Step(`^an admission token is returned after claiming$`, q.claimAndGetToken)
	sc.Step(`^a queue visitor is admitted and claims$`, q.admitAndClaim)
	sc.Step(`^allocated_units is at least (\d+)$`, q.allocAtLeast)
	sc.Step(`^a queue visitor is admitted with (\d+) reserved units and creates a session$`, q.admitAndSession)
	sc.Step(`^the admitted visitor holds only (\d+) ticket$`, q.holdTickets)
	sc.Step(`^the hold succeeds$`, q.holdSucceeds)
	sc.Step(`^allocated_units decreases by at least (\d+) after hold$`, q.allocDecreasedBy)
}

func (q *queueCtx) seedEvent(cap int) error {
	eID, catID, err := helpers.SeedEvent(context.Background(), q.env.Pool, cap)
	if err != nil {
		return err
	}
	q.eventID = eID
	q.catID = catID
	n, _ := helpers.AllocatedUnits(context.Background(), q.env.Pool, eID)
	q.initialAllocated = n
	return nil
}

func (q *queueCtx) aVisitorJoins() error {
	resp, err := helpers.PostJSON(context.Background(),
		helpers.Queue(q.env, "/api/v1/events/"+q.eventID+"/queue-entries"),
		map[string]any{
			"buyerNik":          "3174030303800003",
			"email":             "c@example.test",
			"emailConfirmation": "c@example.test",
			"consentNik":        true,
		}, nil)
	if err != nil {
		return err
	}
	var body map[string]any
	_ = helpers.DecodeBody(resp, &body)
	data, _ := body["data"].(map[string]any)
	q.admit.EntryID, _ = data["entryId"].(string)
	q.admit.VisitorToken, _ = data["visitorToken"].(string)
	return nil
}

func (q *queueCtx) waitAdmitted(secs int) error {
	if q.admit.EntryID == "" {
		return fmt.Errorf("no entry to wait for")
	}
	deadline := time.Now().Add(time.Duration(secs) * time.Second)
	for time.Now().Before(deadline) {
		sr, err := helpers.GetJSON(context.Background(),
			helpers.Queue(q.env, "/api/v1/queue-entries/me?eventId="+q.eventID),
			map[string]string{"X-Visitor-Token": q.admit.VisitorToken})
		if err == nil {
			var sb map[string]any
			_ = helpers.DecodeBody(sr, &sb)
			if d, _ := sb["data"].(map[string]any); d != nil && d["status"] == "ADMITTED" {
				return nil
			}
		}
		time.Sleep(500 * time.Millisecond)
	}
	return fmt.Errorf("visitor not admitted within %ds", secs)
}

func (q *queueCtx) claimAndGetToken() error {
	cr, err := helpers.PostJSON(context.Background(),
		helpers.Queue(q.env, "/api/v1/queue-entries/"+q.admit.EntryID+"/claim"),
		nil, map[string]string{"X-Visitor-Token": q.admit.VisitorToken})
	if err != nil {
		return err
	}
	var cb map[string]any
	_ = helpers.DecodeBody(cr, &cb)
	q.admit.AdmissionToken = helpers.DataString(cb, "admissionToken")
	if q.admit.AdmissionToken == "" {
		return fmt.Errorf("admissionToken missing from claim response: %v", cb)
	}
	return nil
}

func (q *queueCtx) admitAndClaim() error {
	admit, err := helpers.AdmitVisitor(q.env, q.eventID, "3174040404800004", "d@example.test")
	if err != nil {
		return err
	}
	q.admit = admit
	return nil
}

func (q *queueCtx) allocAtLeast(n int) error {
	alloc, err := helpers.AllocatedUnits(context.Background(), q.env.Pool, q.eventID)
	if err != nil {
		return err
	}
	if alloc < n {
		return fmt.Errorf("expected allocated_units >= %d, got %d", n, alloc)
	}
	return nil
}

func (q *queueCtx) admitAndSession(_ int) error {
	admit, err := helpers.AdmitVisitor(q.env, q.eventID, "3174050505800005", "e@example.test")
	if err != nil {
		return err
	}
	q.admit = admit
	sessionID, err := helpers.CreateSession(q.env, admit.AdmissionToken, admit.VisitorToken)
	if err != nil {
		return err
	}
	q.sessionID = sessionID
	n, _ := helpers.AllocatedUnits(context.Background(), q.env.Pool, q.eventID)
	q.initialAllocated = n
	return nil
}

func (q *queueCtx) holdTickets(n int) error {
	niks := []string{"3174010101800001", "3174020202800002", "3174030303800003"}
	items := make([]map[string]any, n)
	for i := 0; i < n; i++ {
		items[i] = map[string]any{
			"categoryId": q.catID,
			"attendee":   map[string]string{"nik": niks[i%len(niks)], "fullName": fmt.Sprintf("Budi %d", i+1)},
		}
	}
	r := helpers.HoldTickets(q.env, q.sessionID, q.admit.VisitorToken,
		fmt.Sprintf("q-key-%d", time.Now().UnixNano()), items)
	q.resp = &http.Response{StatusCode: r.StatusCode}
	q.body = r.Body
	q.bookingID = r.BookingID
	return nil
}

func (q *queueCtx) holdSucceeds() error {
	if q.resp == nil || (q.resp.StatusCode != 200 && q.resp.StatusCode != 201) {
		raw, _ := json.Marshal(q.body)
		return fmt.Errorf("expected 200/201, got %d: %s", q.resp.StatusCode, raw)
	}
	return nil
}

func (q *queueCtx) allocDecreasedBy(n int) error {
	alloc, err := helpers.AllocatedUnits(context.Background(), q.env.Pool, q.eventID)
	if err != nil {
		return err
	}
	decrease := q.initialAllocated - alloc
	if decrease < n {
		return fmt.Errorf("expected allocated_units to decrease by >= %d; initial=%d, now=%d, decrease=%d",
			n, q.initialAllocated, alloc, decrease)
	}
	return nil
}
