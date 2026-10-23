package usecase

import (
	"context"

	"github.com/tiket-sampai-nonton/rusuh-booking-be/pkg/clock"
	pkgnik "github.com/tiket-sampai-nonton/rusuh-booking-be/pkg/nik"

	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/booking/internal/booking/domain/dto"
	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/booking/internal/booking/domain/entity"
	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/booking/internal/booking/domain/mapper"
	vo "github.com/tiket-sampai-nonton/rusuh-booking-be/services/booking/internal/booking/domain/valueobject"
	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/booking/internal/booking/port/inbound"
	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/booking/internal/booking/port/outbound"
)

// Edit replaces a booking's items before payment. payment_expires_at is never reset (AC8.1). Guards:
// locked (payment started), version conflict, and edit-limit are enforced inside the unit of work's
// transaction, but cheap pre-checks happen here on the read model for a fast, clear error.
type Edit struct {
	bookings   outbound.IBookingRepo
	admissions outbound.IAdmissionRepo
	events     outbound.IEventRepo
	uow        outbound.IUnitOfWork
	nik        vo.NIKProtector
	clock      clock.IClock
}

// NewEdit wires the edit usecase.
func NewEdit(bookings outbound.IBookingRepo, admissions outbound.IAdmissionRepo, events outbound.IEventRepo,
	uow outbound.IUnitOfWork, nik vo.NIKProtector, clk clock.IClock) *Edit {
	return &Edit{bookings: bookings, admissions: admissions, events: events, uow: uow, nik: nik, clock: clk}
}

// ReplaceItems validates and applies the edit.
func (uc *Edit) ReplaceItems(ctx context.Context, cmd inbound.EditCommand) (dto.Booking, error) {
	if !isUUID(cmd.BookingID) {
		return dto.Booking{}, inbound.Validation("bookingId", "bookingId harus UUID")
	}
	if len(cmd.Request.Items) == 0 {
		return dto.Booking{}, inbound.Validation("items", "minimal satu item")
	}
	booking, err := uc.bookings.GetByID(ctx, cmd.BookingID)
	if err != nil {
		return dto.Booking{}, err
	}
	if cmd.VisitorID != "" && booking.VisitorID != cmd.VisitorID {
		return dto.Booking{}, inbound.ErrVisitorMismatch
	}
	if booking.IsLocked() {
		return dto.Booking{}, inbound.ErrBookingLocked
	}
	if int(booking.Version) != cmd.Request.Version {
		return dto.Booking{}, inbound.ErrVersionConflict
	}

	admission, err := uc.admissions.GetByID(ctx, booking.AdmissionID)
	if err != nil {
		return dto.Booking{}, err
	}
	if !admission.CanEdit() {
		if admission.EditsRemaining() <= 0 {
			return dto.Booking{}, inbound.ErrEditLimit
		}
		return dto.Booking{}, inbound.ErrBookingLocked
	}

	detail, err := uc.events.GetDetail(ctx, booking.EventID)
	if err != nil {
		return dto.Booking{}, err
	}
	lines, amount, err := uc.buildLines(cmd.Request, detail)
	if err != nil {
		return dto.Booking{}, err
	}

	newVersion, err := uc.uow.ReplaceItems(ctx, outbound.ReplaceSpec{
		BookingID:       booking.ID,
		AdmissionID:     booking.AdmissionID,
		EventID:         booking.EventID,
		BuyerNIKHash:    booking.BuyerNIKHash,
		ExpectedVersion: cmd.Request.Version,
		NewAmountMinor:  amount,
		Lines:           lines,
		CorrelationID:   cmd.CorrelationID,
	})
	if err != nil {
		return dto.Booking{}, err
	}

	updated, err := uc.bookings.GetByID(ctx, booking.ID)
	if err != nil {
		return dto.Booking{}, err
	}
	updated.Version = vo.Version(newVersion)
	return mapper.ToBooking(updated, admission.EditsRemaining()-1, uc.clock.Now()), nil
}

func (uc *Edit) buildLines(req dto.ReplaceItemsRequest, detail entity.EventDetail) ([]outbound.HoldLine, int64, error) {
	if len(req.Items) > detail.Event.MaxTicketsPerOrder {
		return nil, 0, inbound.ErrQuantityExceeds
	}
	prices := make(map[string]int64, len(detail.Categories))
	for _, c := range detail.Categories {
		prices[c.Category.ID] = c.Category.PriceMinor
	}
	seenCat := map[string]bool{}
	seenNIK := map[string]bool{}
	lines := make([]outbound.HoldLine, 0, len(req.Items))
	var amount int64
	for _, it := range req.Items {
		price, ok := prices[it.CategoryID]
		if !ok {
			return nil, 0, inbound.ErrCategoryUnavailable
		}
		seenCat[it.CategoryID] = true
		if len(it.Attendee.FullName) < 2 || len(it.Attendee.FullName) > 100 {
			return nil, 0, inbound.Validation("fullName", "fullName harus 2..100 karakter")
		}
		if err := pkgnik.Validate(it.Attendee.NIK, uc.clock.Now()); err != nil {
			return nil, 0, inbound.ErrNIKInvalid
		}
		nik, err := vo.ParseNIK(it.Attendee.NIK, uc.nik)
		if err != nil {
			return nil, 0, inbound.ErrNIKInvalid
		}
		key := string(nik.Hash())
		if seenNIK[key] {
			return nil, 0, inbound.ErrNikDuplicate
		}
		seenNIK[key] = true
		lines = append(lines, outbound.HoldLine{
			CategoryID:        it.CategoryID,
			UnitPriceMinor:    price,
			AttendeeNIKHash:   nik.Hash(),
			AttendeeNIKEnc:    nik.Enc(),
			AttendeeNIKMasked: nik.Masked(),
			AttendeeName:      it.Attendee.FullName,
		})
		amount += price
	}
	if !detail.Event.AllowMixedCategories && len(seenCat) > 1 {
		return nil, 0, inbound.ErrMixedNotAllowed
	}
	return lines, amount, nil
}
