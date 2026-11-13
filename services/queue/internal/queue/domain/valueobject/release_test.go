package valueobject_test

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/queue/internal/queue/domain/valueobject"
)

// design §5.4: release iff the script itself reported the entry as provably terminal (or ADMITTED_OTHER).
func TestShouldRelease(t *testing.T) {
	tests := []struct {
		status string
		want   bool
	}{
		{"NO_ENTRY", true}, {"LEFT", true}, {"ENDED", true}, {"COMPLETED", true}, {"MISSED", true}, {"ADMITTED_OTHER", true}, {"SOLD_OUT", true},
		{"ADMITTED_SAME", false}, // the entry is ADMITTED with this very allocation: releasing would undo a good admission
		{"NOT_HEAD", false},      // deferred: the row is replayed by the promoter
		{"FULL", false},
		{"", false},
		{"WAITING", false},
		{"STALE", false}, // liveness-only, not terminal: a STALE entry may still revive
		{"SOMETHING_NEW", false},
	}
	for _, tt := range tests {
		t.Run(tt.status, func(t *testing.T) {
			assert.Equal(t, tt.want, valueobject.ShouldRelease(tt.status))
		})
	}
}
