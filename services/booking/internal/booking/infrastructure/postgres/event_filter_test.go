package postgres

import (
	"regexp"
	"strconv"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/booking/internal/booking/domain/dto"
)

// tokenRe extracts positional placeholder tokens ($1, $12, ...) from a SQL fragment. Independent of the
// production placeholderRe so the test does not assume the implementation's own regex is correct.
var tokenRe = regexp.MustCompile(`\$\d+`)

// tokenNums returns the numeric value of every placeholder occurrence, in order of appearance. The predicate
// references each param more than once (e.g. $1 and $3 several times), so this is a multiset, not a set.
func tokenNums(s string) []int {
	toks := tokenRe.FindAllString(s, -1)
	out := make([]int, len(toks))
	for i, tok := range toks {
		n, err := strconv.Atoi(tok[1:])
		if err != nil {
			panic("non-numeric placeholder: " + tok)
		}
		out[i] = n
	}
	return out
}

// TestFilterWhereAt_RenumbersWithoutCorruption pins the exact bug caught during the facets refactor: a
// naive per-number string replace corrupts multi-digit placeholders (e.g. turning the $1 inside a larger
// token to garbage). The invariant: filterWhereAt(base) must shift EVERY placeholder occurrence by exactly
// base, preserving order and multiplicity. Asserting on (token - base) == base-0 multiset proves no token
// was lost, duplicated, or renumbered by the wrong amount — without hardcoding the predicate's internal
// placeholder layout (which legitimately repeats $1, $3, etc.).
func TestFilterWhereAt_RenumbersWithoutCorruption(t *testing.T) {
	want := tokenNums(filterWhereAt(0)) // the ground-truth multiset, in order

	for _, base := range []int{6, 12, 18, 60, 120} {
		t.Run("shift by "+strconv.Itoa(base), func(t *testing.T) {
			got := tokenNums(filterWhereAt(base))
			require.Len(t, got, len(want), "placeholder occurrence count must be preserved (none lost/duplicated), base=%d", base)
			for i := range got {
				// Each occurrence shifted by exactly base: got[i] - base == want[i]. This is the assertion
				// a naive multi-digit-corrupting replace would fail (bases 60/120 force multi-digit results).
				assert.Equal(t, want[i]+base, got[i], "occurrence %d must be original %d shifted by %d", i, want[i], base)
				assert.Positive(t, got[i], "must never emit $0 or negative, base=%d", base)
			}
		})
	}
}

// TestFilterWhereAt_Base0IsConstant asserts the fast-path branch returns the shared constant byte-for-byte,
// so the single-source predicate is reused verbatim (not a re-rendered copy that could drift).
func TestFilterWhereAt_Base0IsConstant(t *testing.T) {
	assert.Equal(t, eventFilterWhere, filterWhereAt(0))
}

// TestFilterWhereAt_NonZeroDiffersFromConstant guards that a non-zero base actually renumbers (the shifted
// form must not equal the original), so a regression that silently ignores base would be caught.
func TestFilterWhereAt_NonZeroDiffersFromConstant(t *testing.T) {
	assert.NotEqual(t, eventFilterWhere, filterWhereAt(6))
	assert.NotEqual(t, eventFilterWhere, filterWhereAt(12))
}

// TestOrAll covers the empty-as-ALL convenience and passthrough.
func TestOrAll(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string
	}{
		{"empty becomes ALL", "", "ALL"},
		{"upcoming passes through", "UPCOMING", "UPCOMING"},
		{"available passes through", "AVAILABLE", "AVAILABLE"},
		{"already ALL stays ALL", "ALL", "ALL"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.want, orAll(tc.in))
		})
	}
}

// TestGenresParam ensures nil becomes a non-nil empty slice (so pgx binds a real text[] and cardinality 0
// means "no filter"), while a populated slice passes through unchanged.
func TestGenresParam(t *testing.T) {
	t.Run("nil becomes non-nil empty slice", func(t *testing.T) {
		got := genresParam(nil)
		require.NotNil(t, got, "must be a real empty text[], not nil")
		assert.Len(t, got, 0)
	})
	t.Run("populated slice passes through", func(t *testing.T) {
		assert.Equal(t, []string{"POP", "ROCK"}, genresParam([]string{"POP", "ROCK"}))
	})
	t.Run("empty non-nil slice passes through", func(t *testing.T) {
		got := genresParam([]string{})
		require.NotNil(t, got)
		assert.Len(t, got, 0)
	})
}

// TestFilterArgs locks the positional arg-order contract that eventFilterWhere ($1..$6) and the Facets /
// ListEvents bindings depend on. A silent reorder here would bind values to the wrong predicate.
func TestFilterArgs(t *testing.T) {
	start := time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC)
	end := time.Date(2026, 11, 8, 0, 0, 0, 0, time.UTC)

	t.Run("full query maps each position", func(t *testing.T) {
		args := filterArgs(dto.EventListQuery{
			Status:    "AVAILABLE",
			City:      "Jakarta",
			Timeframe: "UPCOMING",
			Genres:    []string{"POP"},
			StartUTC:  &start,
			EndUTC:    &end,
		})
		require.Len(t, args, 6, "eventFilterWhere binds exactly six positional args")
		assert.Equal(t, "AVAILABLE", args[0], "$1 status (via orAll)")
		assert.Equal(t, "Jakarta", args[1], "$2 city")
		assert.Equal(t, "UPCOMING", args[2], "$3 timeframe (via orAll)")
		assert.Equal(t, []string{"POP"}, args[3], "$4 genres (via genresParam)")
		assert.Equal(t, &start, args[4], "$5 startUTC")
		assert.Equal(t, &end, args[5], "$6 endUTC")
	})

	t.Run("empty query normalises status and timeframe to ALL and genres to non-nil", func(t *testing.T) {
		args := filterArgs(dto.EventListQuery{})
		require.Len(t, args, 6)
		assert.Equal(t, "ALL", args[0], "empty status -> ALL")
		assert.Equal(t, "", args[1], "empty city stays empty (predicate treats '' as no filter)")
		assert.Equal(t, "ALL", args[2], "empty timeframe -> ALL")
		require.NotNil(t, args[3], "$4 must be a non-nil text[]")
		assert.Equal(t, []string{}, args[3])
		assert.Nil(t, args[4], "$5 open lower bound")
		assert.Nil(t, args[5], "$6 open upper bound")
	})
}
