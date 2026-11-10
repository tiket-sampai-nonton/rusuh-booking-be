package usecase_test

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"

	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/booking/internal/booking/application/usecase"
	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/booking/internal/booking/domain/entity"
	vo "github.com/tiket-sampai-nonton/rusuh-booking-be/services/booking/internal/booking/domain/valueobject"
	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/booking/internal/booking/port/inbound"
)

const otherVisitor = "99999999-9999-9999-9999-999999999999"

// D1: GET /booking-sessions/{id} must reject a missing or foreign visitor (fail closed).
func TestSession_GetSession_Ownership(t *testing.T) {
	tests := []struct {
		name    string
		visitor string
		wantErr error
	}{
		{"missing identity", "", inbound.ErrUnauthorized},
		{"foreign visitor", otherVisitor, inbound.ErrVisitorMismatch},
		{"owner", visitor, nil},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			uc := usecase.NewSession(&fakeAdmissionRepo{admission: activeAdmission(true)}, &fakeEventRepo{detail: detailWith(true, 3)}, &fakeTokenVerifier{}, fixedClock{t: time.Now()})
			_, err := uc.GetSession(context.Background(), sessionID, tc.visitor)
			if tc.wantErr == nil {
				assert.NoError(t, err)
				return
			}
			assert.ErrorIs(t, err, tc.wantErr)
		})
	}
}

// D2: PUT /bookings/{id}/items must reject a missing or foreign visitor and never reach the unit of work.
func TestEdit_Ownership(t *testing.T) {
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
			bk := entity.Booking{ID: bookingID, EventID: eventID, AdmissionID: sessionID, VisitorID: visitor, Status: vo.BookingPendingPayment, Version: 1}
			uow := &fakeUoW{replaceVer: 2}
			cmd := editCmd(1)
			cmd.VisitorID = tc.visitor
			_, err := editUC(bk, activeAdmission(true), uow).ReplaceItems(context.Background(), cmd)
			assert.ErrorIs(t, err, tc.wantErr)
			assert.Zero(t, uow.replaceCalls, "unit of work must not run for a rejected caller")
		})
	}
}

// D4: hold without a visitor identity is a 4xx before any idempotency/DB access.
func TestHold_MissingVisitor(t *testing.T) {
	idem := &fakeIdem{}
	uc := newHoldUC(&fakeAdmissionRepo{admission: admission3()}, &fakeEventRepo{detail: detailWith(true, 3)}, idem, &fakeUoW{})
	cmd := holdCmd(oneItem())
	cmd.VisitorID = ""
	_, err := uc.Hold(context.Background(), cmd)
	assert.ErrorIs(t, err, inbound.ErrUnauthorized)
	assert.Zero(t, idem.begins, "idempotency store (DB) must not be reached")
}
