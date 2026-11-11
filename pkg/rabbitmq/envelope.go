package rabbitmq

import (
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"time"
)

// Envelope is the event envelope (SDD §4.6.2). Tidak memuat NIK plaintext, secret, atau token.
type Envelope struct {
	MessageID     string          `json:"messageId"`
	EventType     string          `json:"eventType"`
	SchemaVersion int             `json:"schemaVersion"`
	OccurredAt    time.Time       `json:"occurredAt"`
	Producer      string          `json:"producer"`
	AggregateID   string          `json:"aggregateId"`
	CorrelationID string          `json:"correlationId,omitempty"`
	Payload       json.RawMessage `json:"payload"`
}

// NewEnvelope builds an envelope with a fresh message id.
func NewEnvelope(eventType, producer, aggregateID, correlationID string, payload any, at time.Time) (Envelope, error) {
	raw, err := json.Marshal(payload)
	if err != nil {
		return Envelope{}, fmt.Errorf("rabbitmq: marshal payload %s: %w", eventType, err)
	}
	return Envelope{
		MessageID: NewID(), EventType: eventType, SchemaVersion: 1, OccurredAt: at.UTC(),
		Producer: producer, AggregateID: aggregateID, CorrelationID: correlationID, Payload: raw,
	}, nil
}

// Validate checks the mandatory fields.
func (e Envelope) Validate() error {
	var errs []error
	if e.MessageID == "" {
		errs = append(errs, errors.New("messageId kosong"))
	}
	if e.EventType == "" {
		errs = append(errs, errors.New("eventType kosong"))
	}
	if e.SchemaVersion < 1 {
		errs = append(errs, errors.New("schemaVersion < 1"))
	}
	if e.Producer == "" {
		errs = append(errs, errors.New("producer kosong"))
	}
	if len(e.Payload) == 0 {
		errs = append(errs, errors.New("payload kosong"))
	}
	if err := errors.Join(errs...); err != nil {
		return fmt.Errorf("rabbitmq: envelope tidak valid: %w", err)
	}
	return nil
}

// NewID returns a UUIDv7 string (RFC 9562, ADR-032): 48-bit Unix milliseconds, then 74 random bits. Ids sort by
// creation time, so B-tree indexes on them stay dense. The creation time is readable from the id; access never depends
// on an id being secret. Ids from the same millisecond are unique but not ordered among themselves.
func NewID() string { return newIDAt(time.Now()) }

func newIDAt(t time.Time) string {
	var r [10]byte // 80 random bits: 4 version + 12 random, 2 variant + 14 random, 48 random
	_, _ = rand.Read(r[:])
	r[0] = (r[0] & 0x0f) | 0x70 // version 7
	r[2] = (r[2] & 0x3f) | 0x80 // variant 10
	// The 48-bit millisecond clock is printed straight as 12 hex digits: no integer conversion to overflow.
	ts := fmt.Sprintf("%012x", t.UnixMilli()&0xffffffffffff)
	return fmt.Sprintf("%s-%s-%x-%x-%x", ts[:8], ts[8:], r[0:2], r[2:4], r[4:10])
}
