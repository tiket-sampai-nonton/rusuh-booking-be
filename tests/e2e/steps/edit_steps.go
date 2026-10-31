//go:build e2e

package steps

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"sync"
	"time"

	"github.com/cucumber/godog"
	"github.com/tiket-sampai-nonton/rusuh-booking-be/tests/e2e/helpers"
)

type editCtx struct {
	env              *helpers.Env
	eventID          string
	catID            string
	admit            helpers.AdmitResult
	sessionID        string
	bookingID        string
	bookingVersion   int
	paymentExpiresAt string
	resp             *http.Response
	body             map[string]any
	editStatuses     []int
}

// RegisterEdit registers edit order step definitions.
func RegisterEdit(sc *godog.ScenarioContext, env *helpers.Env) {
	e := &editCtx{env: env}

	sc.After(func(ctx context.Context, _ *godog.Scenario, _ error) (context.Context, error) {
		if e.eventID != "" {
			_ = helpers.CleanupEvent(ctx, env.Pool, e.eventID)
			e.eventID = ""
		}
		e.admit, e.sessionID, e.bookingID = helpers.AdmitResult{}, "", ""
		e.resp, e.body, e.editStatuses = nil, nil, nil
		e.bookingVersion = 0
		e.paymentExpiresAt = ""
		return ctx, nil
	})

	sc.Step(`^a seeded edit event with (\d+) seats$`, e.seedEvent)
	sc.Step(`^a seeded edit event with (\d+) seats and max_edits=(\d+)$`, e.seedEventMaxEdits)
	sc.Step(`^an edit visitor holds (\d+) ticket$`, e.holdOne)
	sc.Step(`^the edit visitor replaces items with a different NIK$`, e.replaceItems)
	sc.Step(`^the edit succeeds with status (\d+)$`, e.editSucceedsStatus)
	sc.Step(`^the booking version is greater than (\d+)$`, e.versionGreaterThan)
	sc.Step(`^the payment TTL was recorded before the edit$`, e.ttlRecorded)
	sc.Step(`^the edit visitor edits (\d+) times$`, e.editNTimes)
	sc.Step(`^the (\d+)(?:st|nd|rd|th) edit returns status (\d+)$`, e.nthEditStatus)
	sc.Step(`^a payment-lock is applied on the edit booking$`, e.applyPaymentLock)
	sc.Step(`^the edit visitor tries to edit the locked booking$`, e.tryEdit)
	sc.Step(`^the edit response status is (\d+)$`, e.editRespStatus)
	sc.Step(`^the edit error code is "([^"]*)"$`, e.editErrorCode)
	sc.Step(`^two concurrent edits are sent with the same version$`, e.twoConcurrentEdits)
	sc.Step(`^exactly one edit returns 200 and one returns 409$`, e.oneSuccessOneConflict)
}

var editNIKs = []string{"3174010101800001", "3174020202800002"}

func (e *editCtx) seedEvent(cap int) error {
	eID, catID, err := helpers.SeedEvent(context.Background(), e.env.Pool, cap)
	e.eventID = eID
	e.catID = catID
	return err
}

func (e *editCtx) seedEventMaxEdits(cap, maxEdits int) error {
	if err := e.seedEvent(cap); err != nil {
		return err
	}
	_, err := e.env.Pool.Exec(context.Background(),
		`UPDATE booking.events SET max_selection_edits=$1 WHERE id=$2::uuid`, maxEdits, e.eventID)
	return err
}

func (e *editCtx) holdOne(_ int) error {
	admit, err := helpers.AdmitVisitor(e.env, e.eventID, "3174010101800001", "editor@example.test")
	if err != nil {
		return err
	}
	e.admit = admit
	sessionID, err := helpers.CreateSession(e.env, admit.AdmissionToken, admit.VisitorToken)
	if err != nil {
		return err
	}
	e.sessionID = sessionID
	r := helpers.HoldTickets(e.env, sessionID, admit.VisitorToken,
		fmt.Sprintf("edit-key-%d", time.Now().UnixNano()),
		[]map[string]any{{"categoryId": e.catID, "attendee": map[string]string{"nik": "3174010101800001", "fullName": "Budi"}}})
	if r.StatusCode != 201 {
		raw, _ := json.Marshal(r.Body)
		return fmt.Errorf("hold failed: %d %s", r.StatusCode, raw)
	}
	e.bookingID = r.BookingID
	if d, ok := r.Body["data"].(map[string]any); ok {
		if v, ok := d["version"].(float64); ok {
			e.bookingVersion = int(v)
		}
		e.paymentExpiresAt, _ = d["paymentExpiresAt"].(string)
	}
	return nil
}

func (e *editCtx) doEdit(nik string, version int) (int, map[string]any) {
	resp, err := helpers.PutJSON(context.Background(),
		helpers.Booking(e.env, "/api/v1/bookings/"+e.bookingID+"/items"),
		map[string]any{
			"version": version,
			"items":   []map[string]any{{"categoryId": e.catID, "attendee": map[string]string{"nik": nik, "fullName": "Edited"}}},
		},
		map[string]string{"X-Visitor-Token": e.admit.VisitorToken})
	if err != nil {
		return 0, nil
	}
	var body map[string]any
	_ = helpers.DecodeBody(resp, &body)
	return resp.StatusCode, body
}

func (e *editCtx) replaceItems() error {
	status, body := e.doEdit("3174020202800002", e.bookingVersion)
	e.resp = &http.Response{StatusCode: status}
	e.body = body
	if d, ok := body["data"].(map[string]any); ok {
		if v, ok := d["version"].(float64); ok {
			e.bookingVersion = int(v)
		}
	}
	return nil
}

func (e *editCtx) editSucceedsStatus(expected int) error {
	if e.resp == nil {
		return fmt.Errorf("no response")
	}
	if e.resp.StatusCode != expected {
		raw, _ := json.Marshal(e.body)
		return fmt.Errorf("expected %d, got %d: %s", expected, e.resp.StatusCode, raw)
	}
	return nil
}

func (e *editCtx) versionGreaterThan(n int) error {
	if e.bookingVersion <= n {
		return fmt.Errorf("expected version > %d, got %d", n, e.bookingVersion)
	}
	return nil
}

func (e *editCtx) ttlRecorded() error {
	if e.paymentExpiresAt == "" {
		return fmt.Errorf("paymentExpiresAt was not captured before edit")
	}
	return nil
}

func (e *editCtx) editNTimes(n int) error {
	e.editStatuses = nil
	niks := editNIKs
	for i := 0; i < n; i++ {
		status, body := e.doEdit(niks[i%len(niks)], e.bookingVersion)
		e.editStatuses = append(e.editStatuses, status)
		if status == 200 {
			if d, ok := body["data"].(map[string]any); ok {
				if v, ok := d["version"].(float64); ok {
					e.bookingVersion = int(v)
				}
			}
		}
		e.resp = &http.Response{StatusCode: status}
		e.body = body
	}
	return nil
}

func (e *editCtx) nthEditStatus(nth, expected int) error {
	if nth > len(e.editStatuses) {
		return fmt.Errorf("only %d edits recorded, wanted status of #%d", len(e.editStatuses), nth)
	}
	got := e.editStatuses[nth-1]
	if got != expected {
		return fmt.Errorf("edit #%d: expected status %d, got %d", nth, expected, got)
	}
	return nil
}

func (e *editCtx) applyPaymentLock() error {
	resp, err := helpers.PostJSON(context.Background(),
		helpers.Booking(e.env, "/internal/v1/bookings/"+e.bookingID+"/payment-lock"),
		map[string]any{"attemptKey": fmt.Sprintf("lock-%d", time.Now().UnixNano())},
		map[string]string{"Authorization": "Bearer " + e.env.ServiceToken})
	if err != nil {
		return err
	}
	if resp.StatusCode != 200 {
		body := helpers.BodyString(resp)
		return fmt.Errorf("payment-lock failed: %d %s", resp.StatusCode, body)
	}
	return nil
}

func (e *editCtx) tryEdit() error {
	status, body := e.doEdit("3174020202800002", e.bookingVersion)
	e.resp = &http.Response{StatusCode: status}
	e.body = body
	return nil
}

func (e *editCtx) editRespStatus(expected int) error {
	return e.editSucceedsStatus(expected)
}

func (e *editCtx) editErrorCode(code string) error {
	got := helpers.ErrorCode(e.body)
	if got != code {
		raw, _ := json.Marshal(e.body)
		return fmt.Errorf("expected error code %q, got %q (body: %s)", code, got, raw)
	}
	return nil
}

func (e *editCtx) twoConcurrentEdits() error {
	type result struct{ status int }
	results := make([]result, 2)
	var wg sync.WaitGroup
	v := e.bookingVersion
	for i := 0; i < 2; i++ {
		i := i
		wg.Add(1)
		go func() {
			defer wg.Done()
			status, _ := e.doEdit(editNIKs[i%len(editNIKs)], v)
			results[i] = result{status: status}
		}()
	}
	wg.Wait()
	e.editStatuses = []int{results[0].status, results[1].status}
	return nil
}

func (e *editCtx) oneSuccessOneConflict() error {
	s200, s409 := 0, 0
	for _, s := range e.editStatuses {
		switch s {
		case 200:
			s200++
		case 409:
			s409++
		}
	}
	if s200 != 1 || s409 != 1 {
		return fmt.Errorf("expected 1×200 and 1×409, got statuses %v", e.editStatuses)
	}
	return nil
}
