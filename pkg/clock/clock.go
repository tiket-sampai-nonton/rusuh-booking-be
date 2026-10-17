// Package clock abstracts time so business logic can be tested deterministically.
package clock

import "time"

// IClock returns the current time.
type IClock interface {
	Now() time.Time
}

// System is the real clock (UTC).
type System struct{}

// Now returns the current UTC time.
func (System) Now() time.Time { return time.Now().UTC() }

// Fixed always returns the same instant; use it in tests.
type Fixed struct{ T time.Time }

// Now returns the fixed instant.
func (f Fixed) Now() time.Time { return f.T }
