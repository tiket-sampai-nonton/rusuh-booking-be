package entity

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/payment/internal/payment/domain/valueobject"
)

func name(t *testing.T, s string) valueobject.DependencyName {
	t.Helper()
	n, err := valueobject.NewDependencyName(s)
	require.NoError(t, err)
	return n
}

func TestNewHealth(t *testing.T) {
	at := time.Date(2026, 10, 19, 0, 0, 0, 0, time.UTC)
	tests := []struct {
		name string
		deps []DependencyStatus
		want bool
	}{
		{"no dependencies", nil, true},
		{"all up", []DependencyStatus{{Name: name(t, "postgres"), Up: true}}, true},
		{"one down", []DependencyStatus{{Name: name(t, "postgres"), Up: true}, {Name: name(t, "valkey")}}, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h := NewHealth("payment", tt.deps, at)
			assert.Equal(t, tt.want, h.Up)
			assert.Equal(t, "payment", h.Service)
			assert.Equal(t, at, h.CheckedAt)
		})
	}
}
