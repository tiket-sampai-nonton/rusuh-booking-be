package usecase

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tiket-sampai-nonton/rusuh-booking-be/pkg/apperror"
	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/booking/internal/booking/port/inbound"
)

func TestNormalizeGenres(t *testing.T) {
	tokens := func(n int) []string {
		out := make([]string, n)
		for i := range out {
			out[i] = "g" + strings.Repeat("x", i)
		}
		return out
	}
	tests := []struct {
		name    string
		in      []string
		want    []string
		wantErr bool
	}{
		{"nil", nil, nil, false},
		{"empty", []string{}, nil, false},
		{"all blank", []string{" ", ""}, nil, false},
		{"canonical sorted deduped", []string{"pop", " indie-rock ", "POP", "indie rock", ""}, []string{"INDIE_ROCK", "POP"}, false},
		{"20 accepted", tokens(20), nil, false},
		{"21 rejected", tokens(21), nil, true},
		{"64 byte token accepted", []string{strings.Repeat("a", 64)}, []string{strings.Repeat("A", 64)}, false},
		{"65 byte token rejected", []string{strings.Repeat("a", 65)}, nil, true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := normalizeGenres(tc.in)
			if tc.wantErr {
				require.Error(t, err)
				assertValidationField(t, err, "genres")
				return
			}
			require.NoError(t, err)
			if tc.want != nil {
				assert.Equal(t, tc.want, got)
			} else if len(tc.in) < 20 {
				assert.Nil(t, got)
			} else {
				assert.Len(t, got, 20)
			}
		})
	}
}

// assertValidationField asserts err is a typed VALIDATION_ERROR (400) naming the given field.
func assertValidationField(t *testing.T, err error, field string) {
	t.Helper()
	var ae *apperror.Error
	require.ErrorAs(t, err, &ae)
	assert.Equal(t, inbound.CodeValidation, ae.Code)
	assert.Equal(t, 400, ae.HTTPStatus)
	assert.Contains(t, ae.Details, field)
}
