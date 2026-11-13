package postgres

import (
	pkgpg "github.com/tiket-sampai-nonton/rusuh-booking-be/pkg/postgres"

	inbound "github.com/tiket-sampai-nonton/rusuh-booking-be/services/booking/internal/booking/port/inbound"
)

// mapHoldErr turns the stock and uniqueness constraints that back the hold/edit checks into domain errors.
func mapHoldErr(err error) error {
	if err == nil {
		return nil
	}
	if pkgpg.IsCode(err, pkgpg.CodeCheckViolation) {
		if pkgpg.ConstraintName(err) == "buyer_quotas_check" {
			return inbound.ErrBuyerNikLimitReached
		}
		return inbound.ErrCategoryUnavailable
	}
	if pkgpg.IsCode(err, pkgpg.CodeUniqueViolation) {
		switch pkgpg.ConstraintName(err) {
		case "uq_attendee_nik_per_event":
			return inbound.ErrNikRegistered
		case "uq_booking_item_nik_active":
			return inbound.ErrNikDuplicate
		case "bookings_admission_id_key":
			// Satu admission (sesi) hanya boleh menjadi satu booking; hold kedua dengan key baru bukan 500.
			return inbound.ErrAdmissionToken
		}
	}
	return err
}

// mapEditErr turns the admission edit-count check into inbound.ErrEditLimit.
func mapEditErr(err error) error {
	if pkgpg.IsCode(err, pkgpg.CodeCheckViolation) {
		return inbound.ErrEditLimit
	}
	return err
}
