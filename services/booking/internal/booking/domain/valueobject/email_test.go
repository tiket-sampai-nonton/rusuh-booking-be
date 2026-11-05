package valueobject_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	vo "github.com/tiket-sampai-nonton/rusuh-booking-be/services/booking/internal/booking/domain/valueobject"
)

func TestParseEmail(t *testing.T) {
	tests := []struct {
		in      string
		want    string
		invalid bool
	}{
		{"a@b.co", "a@b.co", false},
		{"  buyer@example.com ", "buyer@example.com", false},
		{"Name <a@b.co>", "", true},
		{"no-at-sign", "", true},
		{"", "", true},
		{"a@", "", true},
	}
	for _, tt := range tests {
		t.Run(tt.in, func(t *testing.T) {
			got, err := vo.ParseEmail(tt.in)
			if tt.invalid {
				assert.ErrorIs(t, err, vo.ErrInvalidEmail)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestEmailsMatchIsCaseInsensitive(t *testing.T) {
	assert.True(t, vo.EmailsMatch("A@B.co", " a@b.CO"))
	assert.False(t, vo.EmailsMatch("a@b.co", "c@b.co"))
}
