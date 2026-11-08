package usecase

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNormalizeCity(t *testing.T) {
	tests := []struct {
		name    string
		in      string
		want    string
		wantErr bool
	}{
		{"empty", "", "", false},
		{"whitespace only", "  \t ", "", false},
		{"trimmed, casing kept", "  Jakarta ", "Jakarta", false},
		{"100 runes accepted", strings.Repeat("a", 100), strings.Repeat("a", 100), false},
		{"101 runes rejected", strings.Repeat("a", 101), "", true},
		{"100 multibyte runes accepted (>100 bytes)", strings.Repeat("é", 100), strings.Repeat("é", 100), false},
		{"101 multibyte runes rejected", strings.Repeat("é", 101), "", true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := normalizeCity(tc.in)
			if tc.wantErr {
				assertValidationField(t, err, "city")
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tc.want, got)
		})
	}
}

func TestNormalizeCity_StaticMessage(t *testing.T) {
	_, err := normalizeCity(strings.Repeat("SECRET", 30))
	require.Error(t, err)
	assert.NotContains(t, err.Error(), "SECRET", "error must not echo the input")
}
