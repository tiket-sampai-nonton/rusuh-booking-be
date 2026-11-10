package usecase

import (
	"sort"
	"strings"

	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/booking/internal/booking/port/inbound"
)

// Genre filter guards (attack-query hardening): cap count and per-token length so a crafted query
// cannot inflate the array param or the cache-key cardinality.
const (
	maxGenres        = 20
	maxGenreTokenLen = 64
)

// normalizeGenres canonicalises genre tokens: trim, drop blanks, uppercase, hyphen/space -> underscore,
// dedupe, and sort (stable cache key). Enforces count/length caps. Vocabulary lives on the frontend
// (US-015) so unknown tokens are accepted and simply match nothing (no backend allowlist).
func normalizeGenres(in []string) ([]string, error) {
	if len(in) == 0 {
		return nil, nil
	}
	seen := make(map[string]struct{}, len(in))
	out := make([]string, 0, len(in))
	for _, raw := range in {
		t := strings.TrimSpace(raw)
		if t == "" {
			continue
		}
		t = strings.ToUpper(t)
		t = strings.NewReplacer("-", "_", " ", "_").Replace(t)
		if len(t) > maxGenreTokenLen {
			return nil, inbound.Validation("genres", "genre token terlalu panjang")
		}
		if _, dup := seen[t]; dup {
			continue
		}
		seen[t] = struct{}{}
		out = append(out, t)
	}
	if len(out) > maxGenres {
		return nil, inbound.Validation("genres", "jumlah genre melebihi batas")
	}
	if len(out) == 0 {
		return nil, nil // semua token kosong -> tidak memfilter
	}
	sort.Strings(out) // canonical order -> equivalent sets collapse to one normalized form
	return out, nil
}
