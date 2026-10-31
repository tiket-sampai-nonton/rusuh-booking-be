//go:build e2e

package helpers

import (
	"context"
	"fmt"
	"time"
)

// AdmitResult holds everything needed after a visitor is admitted.
type AdmitResult struct {
	EntryID        string
	VisitorToken   string
	AdmissionToken string
}

// AdmitVisitor joins an event queue and polls until ADMITTED, then claims the turn.
// nik must be a structurally valid 16-digit NIK per pkg/nik.
// The Queue join body requires: buyerNik, email, emailConfirmation, consentNik.
func AdmitVisitor(env *Env, eventID, nik, email string) (AdmitResult, error) {
	ctx := context.Background()

	// 1. Join queue
	resp, err := PostJSON(ctx,
		Queue(env, "/api/v1/events/"+eventID+"/queue-entries"),
		map[string]any{
			"buyerNik":          nik,
			"email":             email,
			"emailConfirmation": email,
			"consentNik":        true,
		}, nil)
	if err != nil {
		return AdmitResult{}, fmt.Errorf("join: %w", err)
	}
	var joinBody map[string]any
	if err := DecodeBody(resp, &joinBody); err != nil {
		return AdmitResult{}, fmt.Errorf("join decode: %w", err)
	}
	if resp.StatusCode != 201 && resp.StatusCode != 200 {
		return AdmitResult{}, fmt.Errorf("join status %d: %v", resp.StatusCode, joinBody)
	}
	data, _ := joinBody["data"].(map[string]any)
	entryID, _ := data["entryId"].(string)
	visitorToken, _ := data["visitorToken"].(string)

	// 2. Poll until ADMITTED (Queue worker must be running; up to 15 s)
	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		sr, err := GetJSON(ctx,
			Queue(env, "/api/v1/queue-entries/me?eventId="+eventID),
			map[string]string{"X-Visitor-Token": visitorToken})
		if err == nil {
			var sb map[string]any
			_ = DecodeBody(sr, &sb)
			if d, _ := sb["data"].(map[string]any); d != nil && d["status"] == "ADMITTED" {
				break
			}
		}
		time.Sleep(500 * time.Millisecond)
	}

	// 3. Claim turn to get the admission token
	cr, err := PostJSON(ctx,
		Queue(env, "/api/v1/queue-entries/"+entryID+"/claim"),
		nil,
		map[string]string{"X-Visitor-Token": visitorToken})
	if err != nil {
		return AdmitResult{}, fmt.Errorf("claim: %w", err)
	}
	var claimBody map[string]any
	if err := DecodeBody(cr, &claimBody); err != nil {
		return AdmitResult{}, fmt.Errorf("claim decode: %w", err)
	}
	if cr.StatusCode != 200 {
		return AdmitResult{}, fmt.Errorf("claim status %d: %v", cr.StatusCode, claimBody)
	}
	admToken := DataString(claimBody, "admissionToken")
	if admToken == "" {
		return AdmitResult{}, fmt.Errorf("admissionToken missing from claim response: %v", claimBody)
	}

	return AdmitResult{EntryID: entryID, VisitorToken: visitorToken, AdmissionToken: admToken}, nil
}

// CreateSession creates a booking session from an admission token. Returns sessionId.
func CreateSession(env *Env, admissionToken, visitorToken string) (string, error) {
	resp, err := PostJSON(context.Background(),
		Booking(env, "/api/v1/booking-sessions"),
		map[string]any{"admissionToken": admissionToken},
		map[string]string{"X-Visitor-Token": visitorToken})
	if err != nil {
		return "", err
	}
	var body map[string]any
	if err := DecodeBody(resp, &body); err != nil {
		return "", err
	}
	if resp.StatusCode != 201 {
		return "", fmt.Errorf("create session status %d: %v", resp.StatusCode, body)
	}
	id := DataString(body, "sessionId")
	if id == "" {
		return "", fmt.Errorf("sessionId missing from response: %v", body)
	}
	return id, nil
}

// HoldResult holds the outcome of a hold request.
type HoldResult struct {
	StatusCode int
	BookingID  string
	Body       map[string]any
}

// HoldTickets performs a booking hold.
func HoldTickets(env *Env, sessionID, visitorToken, idempotencyKey string, items []map[string]any) HoldResult {
	resp, err := PostJSON(context.Background(),
		Booking(env, "/api/v1/bookings"),
		map[string]any{"sessionId": sessionID, "items": items},
		map[string]string{
			"X-Visitor-Token": visitorToken,
			"Idempotency-Key": idempotencyKey,
		})
	if err != nil {
		return HoldResult{StatusCode: 0}
	}
	var body map[string]any
	_ = DecodeBody(resp, &body)
	r := HoldResult{StatusCode: resp.StatusCode, Body: body}
	if d, ok := body["data"].(map[string]any); ok {
		r.BookingID, _ = d["bookingId"].(string)
	}
	return r
}
