package usecase_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tiket-sampai-nonton/rusuh-booking-be/pkg/apperror"

	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/booking/internal/booking/application/usecase"
	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/booking/internal/booking/domain/dto"
)

func newListUC(repo *fakeEventRepo) *usecase.ListEvents {
	return usecase.NewListEvents(repo, fixedClock{t: time.Date(2026, 11, 1, 12, 0, 0, 0, time.UTC)})
}

func codeOf(t *testing.T, err error) apperror.Code {
	t.Helper()
	var ae *apperror.Error
	require.True(t, errors.As(err, &ae), "bukan apperror: %v", err)
	return ae.Code
}

func TestListEvents_GenreNormalization(t *testing.T) {
	tests := []struct {
		name string
		in   []string
		want []string // nil = no filter
	}{
		{"nil -> no filter (all)", nil, nil},
		{"empty strings dropped -> no filter", []string{"", "  "}, nil},
		{"uppercased + sorted", []string{"rock", "pop"}, []string{"POP", "ROCK"}},
		{"deduped", []string{"POP", "pop", "Pop"}, []string{"POP"}},
		{"hyphen/space -> underscore", []string{"k-pop", "hip hop"}, []string{"HIP_HOP", "K_POP"}},
		{"equivalent sets collapse", []string{"rock,pop,pop"}, nil}, // CSV not split here (handler does); single raw token
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			repo := &fakeEventRepo{}
			_, err := newListUC(repo).ListEvents(context.Background(), dto.EventListQuery{Genres: tc.in})
			require.NoError(t, err)
			if tc.name == "equivalent sets collapse" {
				// A single raw token with commas is not split by the usecase; it is one genre token.
				assert.Equal(t, []string{"ROCK,POP,POP"}, repo.gotList.Genres)
				return
			}
			assert.Equal(t, tc.want, repo.gotList.Genres)
		})
	}
}

func TestListEvents_EquivalentGenreSetsProduceSameNormalizedForm(t *testing.T) {
	repoA := &fakeEventRepo{}
	repoB := &fakeEventRepo{}
	_, errA := newListUC(repoA).ListEvents(context.Background(), dto.EventListQuery{Genres: []string{"POP", "ROCK"}})
	_, errB := newListUC(repoB).ListEvents(context.Background(), dto.EventListQuery{Genres: []string{"rock", "pop", "POP"}})
	require.NoError(t, errA)
	require.NoError(t, errB)
	assert.Equal(t, repoA.gotList.Genres, repoB.gotList.Genres, "set genre setara -> bentuk ternormalisasi identik (cache key stabil)")
}

func TestListEvents_GenreCapsRejected(t *testing.T) {
	t.Run("over count cap -> 400", func(t *testing.T) {
		g := make([]string, 21)
		for i := range g {
			g[i] = "G" + string(rune('A'+i))
		}
		_, err := newListUC(&fakeEventRepo{}).ListEvents(context.Background(), dto.EventListQuery{Genres: g})
		require.Error(t, err)
		assert.Equal(t, apperror.CodeValidationFailed, codeOf(t, err))
	})
	t.Run("over token length -> 400", func(t *testing.T) {
		_, err := newListUC(&fakeEventRepo{}).ListEvents(context.Background(), dto.EventListQuery{Genres: []string{strings.Repeat("x", 65)}})
		require.Error(t, err)
		assert.Equal(t, apperror.CodeValidationFailed, codeOf(t, err))
	})
	t.Run("exactly at caps -> ok", func(t *testing.T) {
		g := make([]string, 20)
		for i := range g {
			g[i] = "G" + string(rune('A'+i))
		}
		_, err := newListUC(&fakeEventRepo{}).ListEvents(context.Background(), dto.EventListQuery{Genres: g})
		require.NoError(t, err)
		_, err = newListUC(&fakeEventRepo{}).ListEvents(context.Background(), dto.EventListQuery{Genres: []string{strings.Repeat("x", 64)}})
		require.NoError(t, err)
	})
}

func TestListEvents_EnumsFailSoft(t *testing.T) {
	repo := &fakeEventRepo{}
	_, err := newListUC(repo).ListEvents(context.Background(), dto.EventListQuery{
		Timeframe: "bogus", Status: "nonsense", Sort: "whatever",
	})
	require.NoError(t, err)
	assert.Equal(t, "ALL", repo.gotList.Timeframe)
	assert.Equal(t, "ALL", repo.gotList.Status)
	assert.Equal(t, "DATE_ASC", repo.gotList.Sort)
}

func TestListEvents_DateDescAccepted(t *testing.T) {
	repo := &fakeEventRepo{}
	_, err := newListUC(repo).ListEvents(context.Background(), dto.EventListQuery{Sort: "date_desc"})
	require.NoError(t, err)
	assert.Equal(t, "DATE_DESC", repo.gotList.Sort)
}

func TestListEvents_DateRangeResolvesHalfOpenWIBWindow(t *testing.T) {
	loc, err := time.LoadLocation("Asia/Jakarta")
	require.NoError(t, err)

	t.Run("both bounds -> [startWIBmidnight, endWIBmidnight+1day) in UTC", func(t *testing.T) {
		repo := &fakeEventRepo{}
		_, err := newListUC(repo).ListEvents(context.Background(), dto.EventListQuery{
			StartDate: "2026-12-01", EndDate: "2026-12-31",
		})
		require.NoError(t, err)
		require.NotNil(t, repo.gotList.StartUTC)
		require.NotNil(t, repo.gotList.EndUTC)
		wantStart := time.Date(2026, 12, 1, 0, 0, 0, 0, loc).UTC()
		wantEnd := time.Date(2027, 1, 1, 0, 0, 0, 0, loc).UTC() // end day fully included (half-open)
		assert.True(t, repo.gotList.StartUTC.Equal(wantStart), "start=%v want=%v", repo.gotList.StartUTC, wantStart)
		assert.True(t, repo.gotList.EndUTC.Equal(wantEnd), "end=%v want=%v", repo.gotList.EndUTC, wantEnd)
	})

	t.Run("only start", func(t *testing.T) {
		repo := &fakeEventRepo{}
		_, err := newListUC(repo).ListEvents(context.Background(), dto.EventListQuery{StartDate: "2026-12-01"})
		require.NoError(t, err)
		assert.NotNil(t, repo.gotList.StartUTC)
		assert.Nil(t, repo.gotList.EndUTC)
	})

	t.Run("only end", func(t *testing.T) {
		repo := &fakeEventRepo{}
		_, err := newListUC(repo).ListEvents(context.Background(), dto.EventListQuery{EndDate: "2026-12-31"})
		require.NoError(t, err)
		assert.Nil(t, repo.gotList.StartUTC)
		assert.NotNil(t, repo.gotList.EndUTC)
	})

	t.Run("timeframe and range both propagate (ANDed downstream)", func(t *testing.T) {
		repo := &fakeEventRepo{}
		_, err := newListUC(repo).ListEvents(context.Background(), dto.EventListQuery{
			Timeframe: "THIS_MONTH", StartDate: "2026-12-01", EndDate: "2026-12-31",
		})
		require.NoError(t, err)
		assert.Equal(t, "THIS_MONTH", repo.gotList.Timeframe)
		assert.NotNil(t, repo.gotList.StartUTC)
	})
}

func TestListEvents_DateRangeValidation(t *testing.T) {
	tests := []struct {
		name  string
		q     dto.EventListQuery
		isErr bool
	}{
		{"bad start format", dto.EventListQuery{StartDate: "nonsense"}, true},
		{"bad end format", dto.EventListQuery{EndDate: "2026/12/31"}, true},
		{"inverted range", dto.EventListQuery{StartDate: "2026-12-31", EndDate: "2026-12-01"}, true},
		{"equal bounds ok", dto.EventListQuery{StartDate: "2026-12-01", EndDate: "2026-12-01"}, false},
		{"empty ok", dto.EventListQuery{}, false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			_, err := newListUC(&fakeEventRepo{}).ListEvents(context.Background(), tc.q)
			if tc.isErr {
				require.Error(t, err)
				assert.Equal(t, apperror.CodeValidationFailed, codeOf(t, err))
				return
			}
			require.NoError(t, err)
		})
	}
}
