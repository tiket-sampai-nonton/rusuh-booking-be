package usecase

import (
	"strings"
	"unicode/utf8"

	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/booking/internal/booking/port/inbound"
)

// maxCityRunes bounds the stored city name (events.city is the public filter/facet label).
const maxCityRunes = 100

// normalizeCity trims the admin-supplied city and keeps its casing (the filter is case-insensitive,
// facets show the stored casing). Whitespace-only becomes "" (no city). Over-long input is rejected with a
// static message so the input is never echoed.
func normalizeCity(raw string) (string, error) {
	c := strings.TrimSpace(raw)
	if utf8.RuneCountInString(c) > maxCityRunes {
		return "", inbound.Validation("city", "kota terlalu panjang")
	}
	return c, nil
}
