package mapper_test

import (
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
	d := entity.EventDetail{
		Event: entity.Event{ID: "e1", MaxTicketsPerOrder: 3, AllowMixedCategories: true},
		Categories: []entity.CategoryDetail{
			{Category: entity.Category{ID: "c1", Code: "VIP", PriceMinor: 1000}, Available: 7},
		},
	}
	out := mapper.ToEventDetail(d)
	assert.Equal(t, 3, out.MaxTicketsPerOrder)
	assert.True(t, out.AllowMixedCategories)
	assert.Len(t, out.Categories, 1)
	assert.Equal(t, 7, out.Categories[0].Available)
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
	b.PublicRef, b.BuyerNIKMasked = "RB-AAAA", "3174********0001"
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
	b := entity.Booking{ID: "b1", PublicRef: "RB-7F3A9C21", AmountMinor: 1500000, BuyerNIKMasked: "3174********0009"}
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
