// Package mapper converts domain entities into transport DTOs. Handlers always return DTOs, never
// entities (code-principles.md).
package mapper

import (
	"time"

	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/booking/internal/booking/domain/dto"
	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/booking/internal/booking/domain/entity"
	vo "github.com/tiket-sampai-nonton/rusuh-booking-be/services/booking/internal/booking/domain/valueobject"
)

// ToEventSummary maps a catalog event.
func ToEventSummary(e entity.Event) dto.EventSummary {
	return dto.EventSummary{
		EventID:       e.ID,
		Slug:          e.Slug,
		Name:          e.Name,
		Venue:         e.Venue,
		City:          e.City,
		StartsAt:      e.StartsAt,
		SalesStartsAt: e.SalesStartsAt,
		SalesEndsAt:   e.SalesEndsAt,
		Status:        string(e.Status),
		MinPriceMinor: e.MinPriceMinor,
	}
}

// ToEventList maps a slice of events plus the server clock.
func ToEventList(events []entity.Event, now time.Time) dto.EventList {
	out := dto.EventList{Events: make([]dto.EventSummary, 0, len(events)), ServerTime: now}
	for _, e := range events {
		out.Events = append(out.Events, ToEventSummary(e))
	}
	return out
}

// ToEventDetail maps an event with categories + availability.
func ToEventDetail(d entity.EventDetail) dto.EventDetail {
	cats := make([]dto.CategoryView, 0, len(d.Categories))
	for _, c := range d.Categories {
		cats = append(cats, dto.CategoryView{
			CategoryID: c.Category.ID,
			Code:       c.Category.Code,
			Name:       c.Category.Name,
			PriceMinor: c.Category.PriceMinor,
			Available:  c.Available,
		})
	}
	return dto.EventDetail{
		Event:                 ToEventSummary(d.Event),
		MaxTicketsPerOrder:    d.Event.MaxTicketsPerOrder,
		MaxTicketsPerBuyerNik: d.Event.MaxTicketsPerBuyerNik,
		AllowMixedCategories:  d.Event.AllowMixedCategories,
		Categories:            cats,
	}
}

// ToBooking maps a booking read model to its response DTO.
func ToBooking(b entity.Booking, editsRemaining int, now time.Time) dto.Booking {
	items := make([]dto.BookingItemView, 0, len(b.Items))
	for _, it := range b.Items {
		if it.Status != vo.ItemHeld && it.Status != vo.ItemPaid {
			continue
		}
		items = append(items, dto.BookingItemView{
			ItemID:            it.ID,
			CategoryID:        it.CategoryID,
			AttendeeNIKMasked: it.AttendeeNIKMasked,
			AttendeeName:      it.AttendeeName,
		})
	}
	lockReason := ""
	if b.IsLocked() {
		lockReason = string(vo.LockPaymentStarted)
	}
	return dto.Booking{
		BookingID:        b.ID,
		PublicRef:        b.PublicRef,
		Status:           string(b.Status),
		AmountMinor:      b.AmountMinor,
		Currency:         b.Currency,
		PaymentExpiresAt: b.PaymentExpiresAt,
		ServerTime:       now,
		Version:          int(b.Version),
		CanEdit:          b.CanEdit(),
		EditsRemaining:   editsRemaining,
		LockReason:       lockReason,
		Items:            items,
	}
}

// ToTicketList maps issued tickets to the ticket-list DTO. qrPayloadFn builds the per-ticket QR string.
func ToTicketList(bookingID string, tickets []entity.Ticket, qrPayloadFn func(entity.Ticket) string) dto.TicketList {
	views := make([]dto.TicketView, 0, len(tickets))
	for _, t := range tickets {
		views = append(views, dto.TicketView{
			TicketCode:        t.PublicCode,
			CategoryCode:      t.CategoryCode,
			AttendeeName:      t.AttendeeName,
			AttendeeNIKMasked: t.AttendeeNIKMasked,
			QRPayload:         qrPayloadFn(t),
		})
	}
	return dto.TicketList{BookingID: bookingID, Tickets: views}
}
