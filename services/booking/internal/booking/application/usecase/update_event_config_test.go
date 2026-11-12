package usecase_test

import (
	"context"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/booking/internal/booking/application/usecase"
	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/booking/internal/booking/domain/dto"
	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/booking/internal/booking/port/inbound"
)

func TestUpdateEventConfig_Success(t *testing.T) {
	repo := &fakeEventWriteRepo{}
	uc := usecase.NewUpdateEventConfig(repo)
	err := uc.Update(context.Background(), "ev-1", "acct-7", dto.UpdateEventConfigRequest{})
	require.NoError(t, err)
	assert.Equal(t, "acct-7", repo.updatedBy, "aktor diteruskan ke repo")
}

func TestUpdateEventConfig_RulesLocked(t *testing.T) {
	repo := &fakeEventWriteRepo{updateErr: inbound.ErrRulesLocked}
	uc := usecase.NewUpdateEventConfig(repo)
	err := uc.Update(context.Background(), "ev-1", "acct-7", dto.UpdateEventConfigRequest{})
	assert.ErrorIs(t, err, inbound.ErrRulesLocked)
}

func TestUpdateEventConfig_NoOpIfEmpty(t *testing.T) {
	repo := &fakeEventWriteRepo{}
	uc := usecase.NewUpdateEventConfig(repo)
	// empty request is valid (no-op)
	err := uc.Update(context.Background(), "ev-1", "acct-7", dto.UpdateEventConfigRequest{})
	assert.NoError(t, err)
}

func TestUpdateEventConfig_CityGenres(t *testing.T) {
	strp := func(s string) *string { return &s }
	slp := func(s ...string) *[]string { out := append([]string{}, s...); return &out }
	tooMany := make([]string, 21)
	for i := range tooMany {
		tooMany[i] = "g" + strings.Repeat("x", i)
	}
	tests := []struct {
		name       string
		req        dto.UpdateEventConfigRequest
		wantCity   *string
		wantGenres *[]string
		wantField  string
	}{
		{name: "nil stays nil", req: dto.UpdateEventConfigRequest{}},
		{name: "city trimmed", req: dto.UpdateEventConfigRequest{City: strp("  Bandung ")}, wantCity: strp("Bandung")},
		{name: "city cleared", req: dto.UpdateEventConfigRequest{City: strp("  ")}, wantCity: strp("")},
		{name: "genres canonicalised", req: dto.UpdateEventConfigRequest{Genres: slp("pop", "indie-rock", "")}, wantGenres: slp("INDIE_ROCK", "POP")},
		{name: "empty genres stays non-nil pointer (clear)", req: dto.UpdateEventConfigRequest{Genres: slp()}, wantGenres: slp()},
		{name: "all-blank genres is still a clear", req: dto.UpdateEventConfigRequest{Genres: slp(" ", "")}, wantGenres: slp()},
		{name: "21 genres rejected", req: dto.UpdateEventConfigRequest{Genres: &tooMany}, wantField: "genres"},
		{name: "101 rune city rejected", req: dto.UpdateEventConfigRequest{City: strp(strings.Repeat("k", 101))}, wantField: "city"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			repo := &fakeEventWriteRepo{}
			err := usecase.NewUpdateEventConfig(repo).Update(context.Background(), "ev-1", "acct-7", tc.req)
			if tc.wantField != "" {
				assertValidationField(t, err, tc.wantField)
				assert.Equal(t, 0, repo.updateCalls)
				return
			}
			require.NoError(t, err)
			require.Equal(t, 1, repo.updateCalls)
			assert.Equal(t, tc.wantCity, repo.updatedReq.City)
			if tc.wantGenres == nil {
				assert.Nil(t, repo.updatedReq.Genres)
			} else {
				require.NotNil(t, repo.updatedReq.Genres)
				assert.Equal(t, *tc.wantGenres, *repo.updatedReq.Genres)
			}
		})
	}
}
