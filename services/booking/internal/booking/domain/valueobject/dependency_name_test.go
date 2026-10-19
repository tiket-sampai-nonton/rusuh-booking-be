package valueobject

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestNewDependencyName(t *testing.T) {
	tests := []struct {
		in      string
		wantErr bool
	}{
		{"postgres", false},
		{"rabbit-mq2", false},
		{"", true},
		{"Postgres", true},
		{"9lives", true},
		{"this-name-is-way-too-long-for-a-dependency", true},
	}
	for _, tt := range tests {
		t.Run(tt.in, func(t *testing.T) {
			n, err := NewDependencyName(tt.in)
			if tt.wantErr {
				assert.ErrorIs(t, err, ErrInvalidDependencyName)
				return
			}
			assert.NoError(t, err)
			assert.Equal(t, tt.in, n.String())
		})
	}
}
