package rabbitmq

import (
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ADR-032: NewID mints UUIDv7 (RFC 9562): 48-bit Unix ms, version 7, variant 10, 74 random bits.
func TestNewID_IsUUIDv7WithCreationTime(t *testing.T) {
	v7 := regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-7[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$`)
	at := time.UnixMilli(1_762_840_000_123)
	id := newIDAt(at)
	require.Regexp(t, v7, id)
	ms, err := strconv.ParseInt(strings.ReplaceAll(id, "-", "")[:12], 16, 64)
	require.NoError(t, err)
	assert.Equal(t, at.UnixMilli(), ms, "48 bit pertama = waktu pembuatan (ms)")
	assert.Regexp(t, v7, NewID())
}

// Ids from later milliseconds sort after earlier ones (index locality); random bits keep same-ms ids distinct.
func TestNewID_SortsByTimeAndIsUnique(t *testing.T) {
	base := time.UnixMilli(1_762_840_000_000)
	assert.Less(t, newIDAt(base), newIDAt(base.Add(time.Millisecond)))
	assert.Less(t, newIDAt(base.Add(time.Millisecond)), newIDAt(base.Add(time.Hour)))
	seen := map[string]bool{}
	for i := 0; i < 10_000; i++ {
		id := newIDAt(base)
		require.False(t, seen[id], "id ganda dalam ms yang sama")
		seen[id] = true
	}
}
