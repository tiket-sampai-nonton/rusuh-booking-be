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

// NewID returns a random UUIDv4 string.
func NewID() string {
	var b [16]byte
	_, _ = rand.Read(b[:])
	b[6] = (b[6] & 0x0f) | 0x40
	b[8] = (b[8] & 0x3f) | 0x80
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:16])
}
