package entity_test

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"

	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/booking/internal/booking/domain/entity"
)

func TestAdmission_PastGrace(t *testing.T) {
	expires := time.Date(2026, 11, 13, 10, 0, 0, 0, time.UTC)
	a := entity.Admission{ExpiresAt: expires, CfgSubmitGraceSeconds: 10}

	tests := []struct {
		name string
		now  time.Time
		want bool
	}{
		{"one second before expiry", expires.Add(-time.Second), false},
		{"inside the grace window", expires.Add(5 * time.Second), false},
		{"exactly at grace end", expires.Add(10 * time.Second), true},
		{"after grace end", expires.Add(11 * time.Second), true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, a.PastGrace(tt.now))
		})
	}
}

func TestAdmission_PastGraceWithoutGraceEqualsExpiry(t *testing.T) {
	expires := time.Date(2026, 11, 13, 10, 0, 0, 0, time.UTC)
	a := entity.Admission{ExpiresAt: expires}
	assert.False(t, a.PastGrace(expires.Add(-time.Nanosecond)))
	assert.True(t, a.PastGrace(expires))
	assert.Equal(t, a.IsExpired(expires), a.PastGrace(expires))
}
