package dto_test

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/booking/internal/booking/domain/dto"
)

func TestUpdateEventConfigRequest_CityGenresDecoding(t *testing.T) {
	tests := []struct {
		name       string
		body       string
		wantCity   *string
		wantGenres *[]string
	}{
		{"absent", `{}`, nil, nil},
		{"null", `{"city":null,"genres":null}`, nil, nil},
		{"empty clears", `{"city":"","genres":[]}`, ptr(""), ptr([]string{})},
		{"values", `{"city":"Jakarta","genres":["a","b"]}`, ptr("Jakarta"), ptr([]string{"a", "b"})},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var req dto.UpdateEventConfigRequest
			require.NoError(t, json.Unmarshal([]byte(tc.body), &req))
			assert.Equal(t, tc.wantCity, req.City)
			assert.Equal(t, tc.wantGenres, req.Genres)
		})
	}
}

func TestCreateEventRequest_CityGenresDecoding(t *testing.T) {
	var req dto.CreateEventRequest
	require.NoError(t, json.Unmarshal([]byte(`{"city":"Jakarta","genres":["pop"]}`), &req))
	assert.Equal(t, "Jakarta", req.City)
	assert.Equal(t, []string{"pop"}, req.Genres)

	var empty dto.CreateEventRequest
	require.NoError(t, json.Unmarshal([]byte(`{}`), &empty))
	assert.Empty(t, empty.City)
	assert.Nil(t, empty.Genres)
}

func ptr[T any](v T) *T { return &v }
