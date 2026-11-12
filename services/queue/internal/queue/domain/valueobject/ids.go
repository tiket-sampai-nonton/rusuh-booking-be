package valueobject

import (
	"crypto/rand"
	"errors"
	"fmt"
	"regexp"
	"time"
)

// ErrInvalidID is returned when an id is not a UUID.
var ErrInvalidID = errors.New("id bukan uuid yang valid")

var uuidPattern = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`)

// VisitorID identifies a browser/device (cookie rb_visitor or X-Visitor-Token subject).
type VisitorID struct{ value string }

// NewVisitorID validates s as a UUID.
func NewVisitorID(s string) (VisitorID, error) {
	if !uuidPattern.MatchString(s) {
		return VisitorID{}, ErrInvalidID
	}
	return VisitorID{value: s}, nil
}

// String returns the uuid.
func (v VisitorID) String() string { return v.value }

// EntryID identifies a queue entry.
type EntryID struct{ value string }

// NewEntryID validates s as a UUID.
func NewEntryID(s string) (EntryID, error) {
	if !uuidPattern.MatchString(s) {
		return EntryID{}, ErrInvalidID
	}
	return EntryID{value: s}, nil
}

// String returns the uuid.
func (e EntryID) String() string { return e.value }

// NewUUID returns a UUIDv7 string (RFC 9562, ADR-032): 48-bit Unix milliseconds, then 74 random bits, so entry and
// visitor ids sort by creation time. The domain may only use the standard library (depguard), hence no pkg/rabbitmq.NewID.
// The creation time is readable from the id; access never depends on an id being secret.
func NewUUID() string {
	var r [10]byte // 4 version + 12 random, 2 variant + 14 random, 48 random
	_, _ = rand.Read(r[:])
	r[0] = (r[0] & 0x0f) | 0x70
	r[2] = (r[2] & 0x3f) | 0x80
	ts := fmt.Sprintf("%012x", time.Now().UnixMilli()&0xffffffffffff) // printed as hex: no integer conversion to overflow
	return fmt.Sprintf("%s-%s-%x-%x-%x", ts[:8], ts[8:], r[0:2], r[2:4], r[4:10])
}

// EventID identifies an event (Booking owns the event; Queue only validates the shape).
type EventID struct{ value string }

// NewEventID validates s as a UUID.
func NewEventID(s string) (EventID, error) {
	if !uuidPattern.MatchString(s) {
		return EventID{}, ErrInvalidID
	}
	return EventID{value: s}, nil
}

// String returns the uuid.
func (e EventID) String() string { return e.value }
