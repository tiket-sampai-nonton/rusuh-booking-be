package usecase_test

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/booking/internal/booking/application/usecase"
	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/booking/internal/booking/domain/dto"
	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/booking/internal/booking/domain/entity"
	vo "github.com/tiket-sampai-nonton/rusuh-booking-be/services/booking/internal/booking/domain/valueobject"
	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/booking/internal/booking/port/inbound"
)

const bookingID = "66666666-6666-6666-6666-666666666666"

func editUC(bk entity.Booking, adm entity.Admission, uow *fakeUoW) *usecase.Edit {
	return usecase.NewEdit(
		&fakeBookingRepo{booking: bk},
		&fakeAdmissionRepo{admission: adm},
		&fakeEventRepo{detail: detailWith(true, 3)},
		uow, fakeNIK{}, fixedClock{t: time.Now()},
	)
}

func editCmd(version int) inbound.EditCommand {
	return inbound.EditCommand{
		BookingID: bookingID,
		VisitorID: visitor,
		Request: dto.ReplaceItemsRequest{Version: version, Items: []dto.HoldItem{
			{CategoryID: catA, Attendee: dto.AttendeeInput{NIK: nik1, FullName: "Alice"}},
		}},
	}
}

func TestEdit_VersionConflict(t *testing.T) {
	bk := entity.Booking{ID: bookingID, EventID: eventID, AdmissionID: sessionID, VisitorID: visitor, Status: vo.BookingPendingPayment, Version: 2}
	uc := editUC(bk, activeAdmission(true), &fakeUoW{})
	_, err := uc.ReplaceItems(context.Background(), editCmd(1))
	assert.ErrorIs(t, err, inbound.ErrVersionConflict)
}

func TestEdit_Locked(t *testing.T) {
	started := time.Now()
	bk := entity.Booking{ID: bookingID, EventID: eventID, AdmissionID: sessionID, VisitorID: visitor, Status: vo.BookingPendingPayment, Version: 1, PaymentStartedAt: &started}
	uc := editUC(bk, activeAdmission(true), &fakeUoW{})
	_, err := uc.ReplaceItems(context.Background(), editCmd(1))
	assert.ErrorIs(t, err, inbound.ErrBookingLocked)
}

func TestEdit_EditLimit(t *testing.T) {
	bk := entity.Booking{ID: bookingID, EventID: eventID, AdmissionID: sessionID, VisitorID: visitor, Status: vo.BookingPendingPayment, Version: 1}
	adm := activeAdmission(true)
	adm.EditCount = 3 // == CfgMaxEdits
	uc := editUC(bk, adm, &fakeUoW{})
	_, err := uc.ReplaceItems(context.Background(), editCmd(1))
	assert.ErrorIs(t, err, inbound.ErrEditLimit)
}

func TestEdit_Success(t *testing.T) {
	bk := entity.Booking{ID: bookingID, EventID: eventID, AdmissionID: sessionID, VisitorID: visitor, Status: vo.BookingPendingPayment, Version: 1}
	uow := &fakeUoW{replaceVer: 2}
	uc := usecase.NewEdit(
		&fakeBookingRepo{booking: bk},
		&fakeAdmissionRepo{admission: activeAdmission(true)},
		&fakeEventRepo{detail: detailWith(true, 3)},
		uow, fakeNIK{}, fixedClock{t: time.Now()},
	)
	resp, err := uc.ReplaceItems(context.Background(), editCmd(1))
	require.NoError(t, err)
	assert.Equal(t, 2, resp.Version)
}

func TestSession_CreateTokenInvalid(t *testing.T) {
	uc := usecase.NewSession(&fakeAdmissionRepo{}, &fakeEventRepo{}, &fakeTokenVerifier{err: assertErr{}}, fixedClock{t: time.Now()})
	_, err := uc.CreateSession(context.Background(), dto.CreateSessionRequest{AdmissionToken: "x"}, visitor)
	assert.ErrorIs(t, err, inbound.ErrAdmissionToken)
}

type assertErr struct{}

func (assertErr) Error() string { return "invalid" }
