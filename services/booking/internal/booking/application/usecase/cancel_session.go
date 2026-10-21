package usecase

import (
	"context"

	vo "github.com/tiket-sampai-nonton/rusuh-booking-be/services/booking/internal/booking/domain/valueobject"
	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/booking/internal/booking/port/inbound"
	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/booking/internal/booking/port/outbound"
)

// CancelSession releases an admission allocation when the buyer leaves without buying.
type CancelSession struct{ admissions outbound.IAdmissionRepo }

// NewCancelSession wires the usecase.
func NewCancelSession(admissions outbound.IAdmissionRepo) *CancelSession {
	return &CancelSession{admissions: admissions}
}

// CancelSession releases the admission. Idempotent for terminal admissions.
func (uc *CancelSession) CancelSession(ctx context.Context, sessionID, visitorID string) error {
	adm, err := uc.admissions.GetByID(ctx, sessionID)
	if err != nil {
		return err
	}
	if adm.VisitorID != visitorID {
		return inbound.ErrVisitorMismatch
	}
	// Terminal statuses — idempotent no-op.
	if adm.Status == vo.AdmissionExpired || adm.Status == vo.AdmissionReleased ||
		adm.Status == vo.AdmissionConverted {
		return nil
	}
	_, err = uc.admissions.Release(ctx, adm.ID)
	return err
}
