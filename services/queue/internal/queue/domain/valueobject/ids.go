package valueobject

import (
	"crypto/rand"
	"errors"
	"fmt"
	"regexp"
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

// NewUUID returns a random UUIDv4 string (used to mint entry/visitor ids).
func NewUUID() string {
	var b [16]byte
	_, _ = rand.Read(b[:])
	b[6] = (b[6] & 0x0f) | 0x40
	b[8] = (b[8] & 0x3f) | 0x80
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:16])
}
