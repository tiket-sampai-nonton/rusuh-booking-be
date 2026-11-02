package usecase

import (
	"context"
	"sort"
	"strings"
	"time"

	"github.com/tiket-sampai-nonton/rusuh-booking-be/pkg/clock"

	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/booking/internal/booking/domain/dto"
	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/booking/internal/booking/domain/mapper"
	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/booking/internal/booking/port/inbound"
	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/booking/internal/booking/port/outbound"
)

// Genre filter guards (attack-query hardening): cap count and per-token length so a crafted query
// cannot inflate the array param or the cache-key cardinality.
const (
	maxGenres        = 20
	maxGenreTokenLen = 64
)

// ListEvents serves the public event catalog.
type ListEvents struct {
	events outbound.IEventRepo
	clock  clock.IClock
}

// NewListEvents wires the catalog usecase.
func NewListEvents(events outbound.IEventRepo, clk clock.IClock) *ListEvents {
	return &ListEvents{events: events, clock: clk}
}

// ListEvents normalises the query and returns the catalog.
func (uc *ListEvents) ListEvents(ctx context.Context, q dto.EventListQuery) (dto.EventList, error) {
	q, err := normalizeEventQuery(q)
	if err != nil {
		return dto.EventList{}, err
	}
	events, err := uc.events.ListEvents(ctx, q)
	if err != nil {
		return dto.EventList{}, err
	}
	return mapper.ToEventList(events, uc.clock.Now()), nil
}

// normalizeEventQuery canonicalises filters. Enums fail soft (unknown -> default); structured input
// (genre caps, and dates in Task 4) fails hard with a VALIDATION_ERROR (400).
func normalizeEventQuery(q dto.EventListQuery) (dto.EventListQuery, error) {
	q.Timeframe = oneOf(strings.ToUpper(strings.TrimSpace(q.Timeframe)),
		[]string{"ALL", "THIS_WEEK", "THIS_MONTH", "UPCOMING"}, "ALL")
	q.Status = oneOf(strings.ToUpper(strings.TrimSpace(q.Status)),
		[]string{"ALL", "NOT_STARTED", "AVAILABLE", "SOLD_OUT", "CLOSED"}, "ALL")
	q.Sort = oneOf(strings.ToUpper(strings.TrimSpace(q.Sort)),
		[]string{"DATE_ASC", "DATE_DESC", "PRICE_ASC", "PRICE_DESC"}, "DATE_ASC")
	q.City = strings.TrimSpace(q.City)

	genres, err := normalizeGenres(q.Genres)
	if err != nil {
		return dto.EventListQuery{}, err
	}
	q.Genres = genres

	if err := resolveDateRange(&q); err != nil {
		return dto.EventListQuery{}, err
	}
	return q, nil
}

// wib is the business timezone; date-range bounds are interpreted as WIB calendar days.
var wib = mustLoadWIB()

func mustLoadWIB() *time.Location {
	loc, err := time.LoadLocation("Asia/Jakarta")
	if err != nil {
		return time.FixedZone("WIB", 7*3600) // fallback: fixed +07:00 (no DST in Indonesia)
	}
	return loc
}

// resolveDateRange parses startDate/endDate (strict YYYY-MM-DD, inclusive, WIB) and sets the half-open
// UTC window [startUTC, endUTC) on the query. End is exclusive at WIB-midnight of endDate+1 day so the
// whole end day is included. Bad format or an inverted range fails hard with VALIDATION_ERROR (400).
func resolveDateRange(q *dto.EventListQuery) error {
	const layout = "2006-01-02"
	var startD, endD *time.Time

	if s := strings.TrimSpace(q.StartDate); s != "" {
		d, err := time.ParseInLocation(layout, s, wib)
		if err != nil {
			return inbound.Validation("startDate", "format tanggal harus YYYY-MM-DD")
		}
		startD = &d
	}
	if s := strings.TrimSpace(q.EndDate); s != "" {
		d, err := time.ParseInLocation(layout, s, wib)
		if err != nil {
			return inbound.Validation("endDate", "format tanggal harus YYYY-MM-DD")
		}
		endD = &d
	}
	if startD != nil && endD != nil && endD.Before(*startD) {
		return inbound.Validation("endDate", "endDate tidak boleh sebelum startDate")
	}

	if startD != nil {
		u := startD.UTC()
		q.StartUTC = &u
	}
	if endD != nil {
		u := endD.AddDate(0, 0, 1).UTC() // half-open: include the whole end day
		q.EndUTC = &u
	}
	return nil
}

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

func oneOf(v string, allowed []string, fallback string) string {
	for _, a := range allowed {
		if v == a {
			return v
		}
	}
	return fallback
}
