package valueobject_test

import (
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"

	vo "github.com/tiket-sampai-nonton/rusuh-booking-be/services/queue/internal/queue/domain/valueobject"
)

// TC-ANON-07 (R05.1): G = ceil(F/M), P = ceil((F+A+H)/M), in entries.
func TestZoneCounts(t *testing.T) {
	tests := []struct {
		f, a, h, m int
		g, p       int64
	}{
		{7, 3, 2, 3, 3, 4}, // case 1
		{1, 0, 0, 3, 1, 1}, // case 3
		{0, 6, 0, 3, 0, 2}, // case 4
		{2, 0, 0, 3, 1, 1}, // R05.2: free 1 or 2 still guarantees the front entry
		{0, 0, 0, 3, 0, 0},
		{5, 0, 0, 0, 2, 2}, // M unknown defaults to 3
	}
	for _, tt := range tests {
		t.Run(fmt.Sprintf("F%d_A%d_H%d", tt.f, tt.a, tt.h), func(t *testing.T) {
			g, p := vo.ZoneCounts(tt.f, tt.a, tt.h, tt.m)
			assert.Equal(t, tt.g, g)
			assert.Equal(t, tt.p, p)
		})
	}
}

// One zone rule over SEQ (ADR-030 Amendment: the draw was dropped, seq is the order), including the 0 = empty zone
// meaning.
func TestZoneForSeq(t *testing.T) {
	assert.Equal(t, vo.ZoneGuaranteed, vo.ZoneForSeq(3, 3, 4))
	assert.Equal(t, vo.ZonePossible, vo.ZoneForSeq(4, 3, 4))
	assert.Equal(t, vo.ZoneNotEnough, vo.ZoneForSeq(480, 3, 4), "well past the possible boundary")
	assert.Equal(t, vo.ZoneGuaranteed, vo.ZoneForSeq(2, 3, 4))
	assert.Equal(t, vo.ZonePossible, vo.ZoneForSeq(1, 0, 2), "G=0: nobody guaranteed")
	assert.Equal(t, vo.ZoneNotEnough, vo.ZoneForSeq(1, 0, 0))
}

// R03.1: interval ±20% jitter, short interval near the head.
func TestNextHeartbeatMs(t *testing.T) {
	base, near := int64(30_000), int64(10_000)
	for _, j := range []float64{0, 0.5, 0.999} {
		ms := vo.NextHeartbeatMs(base, near, 500, 1000, 100, j)
		assert.GreaterOrEqual(t, ms, int64(24_000))
		assert.LessOrEqual(t, ms, int64(36_000))
	}
	assert.Equal(t, near, vo.NextHeartbeatMs(base, near, 500, 150, 100, 0.5), "within near seqs of the head")
	assert.Equal(t, base, vo.NextHeartbeatMs(base, near, 500, 0, 0, 0.5), "unknown seq: base interval (no near load spike)")
}
