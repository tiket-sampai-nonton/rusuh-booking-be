package entity_test

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/queue/internal/queue/domain/entity"
	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/queue/internal/queue/domain/valueobject"
)

func TestEntry_ExactPosition(t *testing.T) {
	tests := []struct {
		name         string
		seq          int64
		serving      int64
		removedAhead int64
		want         int64
	}{
		{"no removed", 100, 55, 0, 45},
		{"with removed ahead", 100, 55, 5, 40},
		{"at front", 56, 55, 0, 1},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			e := entity.Entry{Seq: valueobject.Seq(tt.seq)}
			assert.Equal(t, tt.want, e.ExactPosition(tt.serving, tt.removedAhead))
		})
	}
}
