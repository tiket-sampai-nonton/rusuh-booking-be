package usecase

import (
	"context"
	"strings"

	"github.com/tiket-sampai-nonton/rusuh-booking-be/pkg/clock"

	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/booking/internal/booking/domain/dto"
	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/booking/internal/booking/domain/mapper"
	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/booking/internal/booking/port/outbound"
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
	q = normalizeEventQuery(q)
	events, err := uc.events.ListEvents(ctx, q)
	if err != nil {
		return dto.EventList{}, err
	}
	return mapper.ToEventList(events, uc.clock.Now()), nil
}

func normalizeEventQuery(q dto.EventListQuery) dto.EventListQuery {
	q.Timeframe = oneOf(strings.ToUpper(strings.TrimSpace(q.Timeframe)),
		[]string{"ALL", "THIS_WEEK", "THIS_MONTH", "UPCOMING"}, "ALL")
	q.Status = oneOf(strings.ToUpper(strings.TrimSpace(q.Status)),
		[]string{"ALL", "NOT_STARTED", "AVAILABLE", "SOLD_OUT", "CLOSED"}, "ALL")
	q.Sort = oneOf(strings.ToUpper(strings.TrimSpace(q.Sort)),
		[]string{"DATE_ASC", "PRICE_ASC", "PRICE_DESC"}, "DATE_ASC")
	q.City = strings.TrimSpace(q.City)
	return q
}

func oneOf(v string, allowed []string, fallback string) string {
	for _, a := range allowed {
		if v == a {
			return v
		}
	}
	return fallback
}
