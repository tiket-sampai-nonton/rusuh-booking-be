package rabbitmq

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

// TestParseRFC3339 covers both branches of the unexported helper: empty input (zero time) and a real
// RFC3339 string, plus the fail-soft path where an unparseable string yields the zero time rather than
// an error (the consumer treats a missing/garbage timestamp as "unknown", not a hard failure).
func TestParseRFC3339(t *testing.T) {
	t.Run("empty string yields zero time", func(t *testing.T) {
		assert.True(t, parseRFC3339("").IsZero())
	})

	t.Run("valid RFC3339 parses to the instant", func(t *testing.T) {
		got := parseRFC3339("2026-11-03T04:12:48Z")
		assert.False(t, got.IsZero())
		assert.Equal(t, time.Date(2026, 11, 3, 4, 12, 48, 0, time.UTC), got.UTC())
	})

	t.Run("unparseable string fails soft to zero time", func(t *testing.T) {
		assert.True(t, parseRFC3339("not-a-timestamp").IsZero(), "parse error returns zero time, not a panic/error")
	})
}
