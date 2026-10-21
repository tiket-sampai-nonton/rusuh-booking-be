package mapper

import (
	"time"

	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/booking/internal/booking/domain/dto"
	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/booking/internal/booking/domain/entity"
	vo "github.com/tiket-sampai-nonton/rusuh-booking-be/services/booking/internal/booking/domain/valueobject"
)

// ToBookingSession builds a session view from the admission, the event detail, and an optional booking.
func ToBookingSession(a entity.Admission, detail entity.EventDetail, booking *dto.Booking, now time.Time) dto.BookingSession {
	cats := make([]dto.CategoryView, 0, len(detail.Categories))
	for _, c := range detail.Categories {
		cats = append(cats, dto.CategoryView{
			CategoryID: c.Category.ID,
			Code:       c.Category.Code,
			Name:       c.Category.Name,
			PriceMinor: c.Category.PriceMinor,
			Available:  c.Available,
		})
	}
	return dto.BookingSession{
		SessionID:            a.ID,
		EventID:              a.EventID,
		Phase:                phaseOf(a.Status),
		ReservedUnits:        a.Units,
		MaxSelectable:        a.Units,
		BuyerNIKMasked:       a.BuyerNIKMasked,
		AllowMixedCategories: a.CfgAllowMixedCategories,
		ExpiresAt:            a.ExpiresAt,
		ServerTime:           now,
		EditEnabled:          a.CfgEditEnabled,
		MaxEdits:             a.CfgMaxEdits,
		Categories:           cats,
		Booking:              booking,
	}
}

func phaseOf(s vo.AdmissionStatus) string {
	switch s {
	case vo.AdmissionReserved, vo.AdmissionActive:
		return "SELECTING"
	case vo.AdmissionEditable:
		return "EDITABLE"
	case vo.AdmissionConverted:
		return "LOCKED"
	default:
		return "ENDED"
	}
}
