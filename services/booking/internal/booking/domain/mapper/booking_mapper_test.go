package mapper_test

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"

	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/booking/internal/booking/domain/entity"
	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/booking/internal/booking/domain/mapper"
	vo "github.com/tiket-sampai-nonton/rusuh-booking-be/services/booking/internal/booking/domain/valueobject"
)

func TestToEventList(t *testing.T) {
	now := time.Now().UTC()
	events := []entity.Event{{ID: "e1", Name: "Konser", Status: entity.EventAvailable, MinPriceMinor: 500}}
	out := mapper.ToEventList(events, now)
	assert.Len(t, out.Events, 1)
	assert.Equal(t, "e1", out.Events[0].EventID)
	assert.EqualValues(t, 500, out.Events[0].MinPriceMinor)
	assert.Equal(t, now, out.ServerTime)
}

func TestToEventDetail(t *testing.T) {
	now := time.Now().UTC()
	d := entity.EventDetail{
		Event: entity.Event{ID: "e1", MaxTicketsPerOrder: 3, AllowMixedCategories: true, SalesStartsAt: now.Add(-time.Hour), SalesEndsAt: now.Add(time.Hour)},
		Categories: []entity.CategoryDetail{
			{Category: entity.Category{ID: "c1", Code: "VIP", PriceMinor: 1000}, Available: 7},
		},
	}
	out := mapper.ToEventDetail(d, now)
	assert.Equal(t, 3, out.MaxTicketsPerOrder)
	assert.True(t, out.AllowMixedCategories)
	assert.Len(t, out.Categories, 1)
	assert.Equal(t, 7, out.Categories[0].Available)
	assert.Equal(t, "AVAILABLE", out.Event.Status)
}

// The API shows one of NOT_STARTED, AVAILABLE, SOLD_OUT, CLOSED (FE enum). A stored DRAFT/NOT_STARTED/AVAILABLE
// status follows the sales window and the stock, because nothing else ever advances it.
func TestEventStatusFollowsTheSalesWindowAndStock(t *testing.T) {
	now := time.Date(2026, 10, 31, 12, 0, 0, 0, time.UTC)
	ev := func(st entity.EventStatus, from, to time.Duration) entity.Event {
		return entity.Event{ID: "e", Status: st, SalesStartsAt: now.Add(from), SalesEndsAt: now.Add(to)}
	}
	for name, c := range map[string]struct {
		e    entity.Event
		want string
	}{
		"draft before sale":    {ev(entity.EventDraft, time.Hour, 2*time.Hour), "NOT_STARTED"},
		"draft during sale":    {ev(entity.EventDraft, -time.Hour, time.Hour), "AVAILABLE"},
		"available after sale": {ev(entity.EventAvailable, -2*time.Hour, -time.Hour), "CLOSED"},
		"stored sold out wins": {ev(entity.EventSoldOut, -time.Hour, time.Hour), "SOLD_OUT"},
		"stored closed wins":   {ev(entity.EventClosed, -time.Hour, time.Hour), "CLOSED"},
	} {
		assert.Equal(t, c.want, mapper.ToEventList([]entity.Event{c.e}, now).Events[0].Status, name)
	}

	soldOut := entity.EventDetail{Event: ev(entity.EventDraft, -time.Hour, time.Hour), Categories: []entity.CategoryDetail{{Available: 0}, {Available: 0}}}
	assert.Equal(t, "SOLD_OUT", mapper.ToEventDetail(soldOut, now).Event.Status, "tanpa sisa stok di semua kategori")
}

func TestToBooking_OnlyActiveItemsAndLock(t *testing.T) {
	now := time.Now().UTC()
	started := now
	b := entity.Booking{
		ID: "b1", PublicRef: "RB-1", Status: vo.BookingVerifying, AmountMinor: 2000, Currency: "IDR",
		Version: 2, PaymentStartedAt: &started,
		Items: []entity.BookingItem{
			{ID: "i1", CategoryID: "c1", Status: vo.ItemHeld, AttendeeName: "A", AttendeeNIKMasked: "3174********0001"},
			{ID: "i2", CategoryID: "c1", Status: vo.ItemReplaced, AttendeeName: "B"},
		},
	}
	b.PublicRef, b.BuyerNIKMasked, b.ItemQuantity = "RB-AAAA", "3174********0001", 1
	out := mapper.ToBooking(b, 1, now)
	assert.Len(t, out.Items, 1, "REPLACED items are excluded")
	assert.Equal(t, 1, out.TicketQuantity)
	assert.Equal(t, "RB-AAAA", out.OrderID)
	assert.Equal(t, "3174********0001", out.BuyerNIKMasked)
	assert.Equal(t, "i1", out.Items[0].ItemID)
	assert.Equal(t, "PAYMENT_STARTED", out.LockReason)
	assert.False(t, out.CanEdit)
	assert.Equal(t, 2, out.Version)
}

func TestToTicketList(t *testing.T) {
	tickets := []entity.Ticket{{PublicCode: "TK-1", CategoryCode: "VIP", AttendeeName: "A", AttendeeNIKMasked: "3174********0001"}}
	b := entity.Booking{ID: "b1", PublicRef: "RB-7F3A9C21", AmountMinor: 1500000, BuyerNIKMasked: "3174********0009", ItemQuantity: 1}
	out := mapper.ToTicketList(b, tickets, func(tk entity.Ticket) string { return "QR:" + tk.PublicCode })
	assert.Equal(t, "b1", out.BookingID)
	assert.Equal(t, "RB-7F3A9C21", out.OrderID)
	assert.Equal(t, "RB-7F3A9C21", out.PublicRef)
	assert.EqualValues(t, 1500000, out.AmountMinor)
	assert.Equal(t, 1, out.TicketQuantity)
	assert.Equal(t, "3174********0009", out.BuyerNIKMasked)
	assert.Len(t, out.Tickets, 1)
	assert.Equal(t, "QR:TK-1", out.Tickets[0].QRPayload)
}

func TestToEventSummary_CarriesImageURLsOnlyWhenPresent(t *testing.T) {
	now := time.Now().UTC()
	with := mapper.ToEventSummary(entity.Event{ID: "e1", ImageURL: "https://cdn/i.png", BannerURL: "https://cdn/b.png", ThumbnailURL: "https://cdn/t.png"}, now)
	assert.Equal(t, "https://cdn/i.png", with.ImageURL)
	assert.Equal(t, "https://cdn/b.png", with.BannerURL)
	assert.Equal(t, "https://cdn/t.png", with.ThumbnailURL)
	raw, _ := json.Marshal(mapper.ToEventSummary(entity.Event{ID: "e2"}, now))
	assert.NotContains(t, string(raw), "Url", "tanpa gambar: field tidak dikirim")
}
