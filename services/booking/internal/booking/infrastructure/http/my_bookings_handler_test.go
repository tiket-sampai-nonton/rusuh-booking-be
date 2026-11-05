package http

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tiket-sampai-nonton/rusuh-booking-be/pkg/apperror"
)

// TestParseHistoryLimit covers the `limit` query-param validation for GET /api/v1/me/bookings
// (contract: integer, minimum 1, maximum 50, default 20). Out-of-range and non-integer values
// must be rejected with 400 VALIDATION_ERROR rather than silently clamped (Finding A).
func TestParseHistoryLimit(t *testing.T) {
	tests := []struct {
		name    string
		raw     string
		want    int
		wantErr bool
	}{
		{name: "absent uses default (0 -> usecase defaults)", raw: "", want: 0},
		{name: "min boundary 1", raw: "1", want: 1},
		{name: "typical 20", raw: "20", want: 20},
		{name: "max boundary 50", raw: "50", want: 50},
		{name: "zero rejected", raw: "0", wantErr: true},
		{name: "above max rejected", raw: "51", wantErr: true},
		{name: "negative rejected", raw: "-5", wantErr: true},
		{name: "non-integer rejected", raw: "abc", wantErr: true},
		{name: "float rejected", raw: "2.5", wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := parseHistoryLimit(tt.raw)
			if tt.wantErr {
				require.Error(t, err)
				ae := apperror.From(err)
				require.NotNil(t, ae, "error must be an apperror")
				assert.Equal(t, 400, ae.HTTPStatus, "out-of-range/invalid limit must be 400 VALIDATION_ERROR")
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tt.want, got)
		})
	}
}
