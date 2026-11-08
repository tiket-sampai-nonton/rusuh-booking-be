package postgres

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/booking/internal/booking/domain/dto"
)

func TestBuildUpdateConfig_CityGenres(t *testing.T) {
	city := "Jakarta"
	emptyCity := ""
	genres := []string{"POP"}
	noGenres := []string{}
	ttl := 300
	tests := []struct {
		name     string
		req      dto.UpdateEventConfigRequest
		wantSQL  string
		wantArgs []any
	}{
		{"nothing set", dto.UpdateEventConfigRequest{}, "", nil},
		{"city only", dto.UpdateEventConfigRequest{City: &city},
			`UPDATE booking.events SET city = $2 WHERE id = $1::uuid`, []any{"ev", "Jakarta"}},
		{"city cleared", dto.UpdateEventConfigRequest{City: &emptyCity},
			`UPDATE booking.events SET city = $2 WHERE id = $1::uuid`, []any{"ev", ""}},
		{"genres only", dto.UpdateEventConfigRequest{Genres: &genres},
			`UPDATE booking.events SET genre = $2 WHERE id = $1::uuid`, []any{"ev", []string{"POP"}}},
		{"genres cleared binds empty array, not NULL", dto.UpdateEventConfigRequest{Genres: &noGenres},
			`UPDATE booking.events SET genre = $2 WHERE id = $1::uuid`, []any{"ev", []string{}}},
		{"mixed with ttl keeps placeholder order", dto.UpdateEventConfigRequest{SelectionTTLSeconds: &ttl, City: &city, Genres: &genres},
			`UPDATE booking.events SET selection_ttl_seconds = $2, city = $3, genre = $4 WHERE id = $1::uuid`,
			[]any{"ev", 300, "Jakarta", []string{"POP"}}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			sql, args := buildUpdateConfig("ev", tc.req)
			assert.Equal(t, tc.wantSQL, sql)
			if tc.wantSQL == "" {
				assert.Nil(t, args)
				return
			}
			require.Equal(t, tc.wantArgs, args)
		})
	}
}
