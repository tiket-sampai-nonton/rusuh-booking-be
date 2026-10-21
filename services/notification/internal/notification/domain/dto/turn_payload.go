package dto

import (
	"errors"
	"fmt"
	"time"
)

// TurnSoonPayload is the queue.turn.soon.v1 payload (design §10.4). The push token is
// NOT in the payload; it is looked up from push_subscriptions(visitor_id, event_id).
type TurnSoonPayload struct {
	EventID      string `json:"eventId"`
	QueueEntryID string `json:"queueEntryId"`
	VisitorID    string `json:"visitorId"`
}

// Validate enforces the required fields of queue.turn.soon.v1.
func (p TurnSoonPayload) Validate() error {
	var errs []error
	if p.EventID == "" {
		errs = append(errs, errors.New("eventId kosong"))
	}
	if p.QueueEntryID == "" {
		errs = append(errs, errors.New("queueEntryId kosong"))
	}
	if p.VisitorID == "" {
		errs = append(errs, errors.New("visitorId kosong"))
	}
	if err := errors.Join(errs...); err != nil {
		return fmt.Errorf("queue.turn.soon payload tidak valid: %w", err)
	}
	return nil
}

// TurnArrivedPayload is the queue.turn.arrived.v1 payload (design §10.5), with a claim deadline.
type TurnArrivedPayload struct {
	EventID       string `json:"eventId"`
	QueueEntryID  string `json:"queueEntryId"`
	VisitorID     string `json:"visitorId"`
	ClaimDeadline string `json:"claimDeadline"`
}

// Validate enforces the required fields of queue.turn.arrived.v1.
func (p TurnArrivedPayload) Validate() error {
	var errs []error
	if p.EventID == "" {
		errs = append(errs, errors.New("eventId kosong"))
	}
	if p.QueueEntryID == "" {
		errs = append(errs, errors.New("queueEntryId kosong"))
	}
	if p.VisitorID == "" {
		errs = append(errs, errors.New("visitorId kosong"))
	}
	if p.ClaimDeadline == "" {
		errs = append(errs, errors.New("claimDeadline kosong"))
	}
	if err := errors.Join(errs...); err != nil {
		return fmt.Errorf("queue.turn.arrived payload tidak valid: %w", err)
	}
	return nil
}

// ParseClaimDeadline parses the RFC3339 claim deadline.
func (p TurnArrivedPayload) ParseClaimDeadline() (time.Time, error) {
	return time.Parse(time.RFC3339, p.ClaimDeadline)
}
