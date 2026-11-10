package usecase_test

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"

	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/booking/internal/booking/application/usecase"
	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/booking/internal/booking/domain/dto"
	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/booking/internal/booking/domain/entity"
	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/booking/internal/booking/port/inbound"
)

// D3: attendee-checks must be bound to the session owner (no NIK-registration oracle).
func TestCheckAttendee_Ownership(t *testing.T) {
	tests := []struct {
		name    string
		visitor string
		wantErr error
	}{
		{"missing identity", "", inbound.ErrUnauthorized},
		{"foreign visitor", otherVisitor, inbound.ErrVisitorMismatch},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			checker := &fakeAttendeeChecker{counted: true, registered: true}
			uc := usecase.NewCheckAttendee(&fakeAdmissionRepo{admission: activeAdmission(true)}, &fakeEventRepo{event: entity.Event{ID: eventID, MaxAttendeeChecks: 10}}, checker, fakeNIK{}, fixedClock{t: time.Now()})
			_, err := uc.CheckAttendee(context.Background(), sessionID, tc.visitor, dto.AttendeeCheckRequest{NIK: nik1})
			assert.ErrorIs(t, err, tc.wantErr)
			assert.Zero(t, checker.calls, "probe must not run for a rejected caller")
		})
	}
}
