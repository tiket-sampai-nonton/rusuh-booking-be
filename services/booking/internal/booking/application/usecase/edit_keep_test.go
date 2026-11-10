package usecase_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tiket-sampai-nonton/rusuh-booking-be/pkg/apperror"
	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/booking/internal/booking/domain/dto"
	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/booking/internal/booking/domain/entity"
	vo "github.com/tiket-sampai-nonton/rusuh-booking-be/services/booking/internal/booking/domain/valueobject"
	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/booking/internal/booking/port/inbound"
)

const (
	keepA = "aaaaaaaa-0000-0000-0000-00000000000a"
	keepB = "aaaaaaaa-0000-0000-0000-00000000000b"
	keepR = "aaaaaaaa-0000-0000-0000-00000000000c" // REPLACED
	keepX = "aaaaaaaa-0000-0000-0000-00000000000d" // CANCELLED/RELEASED
)

func keepBooking() entity.Booking {
	item := func(id, nik, name string, st vo.ItemStatus) entity.BookingItem {
		return entity.BookingItem{
			ID: id, BookingID: bookingID, EventID: eventID, CategoryID: catA, UnitPriceMinor: 1000,
			AttendeeNIKHash: []byte("h:" + nik), AttendeeNIKEnc: []byte("e:" + nik),
			AttendeeNIKMasked: "3174********" + nik[len(nik)-4:], AttendeeName: name, Status: st,
		}
	}
	return entity.Booking{
		ID: bookingID, EventID: eventID, AdmissionID: sessionID, VisitorID: visitor,
		Status: vo.BookingPendingPayment, Version: 1,
		Items: []entity.BookingItem{
			item(keepA, nik1, "Alice", vo.ItemHeld),
			item(keepB, nik2, "Bob", vo.ItemHeld),
			item(keepR, "3174010101010003", "Old", vo.ItemReplaced),
			item(keepX, "3174010101010004", "Gone", vo.ItemReleased),
		},
	}
}

func keepReq(items ...dto.HoldItem) inbound.EditCommand {
	return inbound.EditCommand{BookingID: bookingID, VisitorID: visitor,
		Request: dto.ReplaceItemsRequest{Version: 1, Items: items}}
}

func keepItem(id string) dto.HoldItem {
	return dto.HoldItem{CategoryID: catA, Attendee: dto.AttendeeInput{KeepFromItemID: id}}
}

func newItem(nik, name string) dto.HoldItem {
	return dto.HoldItem{CategoryID: catA, Attendee: dto.AttendeeInput{NIK: nik, FullName: name}}
}

func TestEdit_Keep_CopiesHolderColumns(t *testing.T) {
	uow := &fakeUoW{replaceVer: 2}
	uc := editUC(keepBooking(), activeAdmission(true), uow)
	_, err := uc.ReplaceItems(context.Background(), keepReq(keepItem(keepA), newItem("3174010101010009", "Carol")))
	require.NoError(t, err)
	require.Equal(t, 1, uow.replaceCalls)
	lines := uow.replaceSpec.Lines
	require.Len(t, lines, 2)
	assert.Equal(t, []byte("h:"+nik1), lines[0].AttendeeNIKHash)
	assert.Equal(t, []byte("e:"+nik1), lines[0].AttendeeNIKEnc)
	assert.Equal(t, "3174********0001", lines[0].AttendeeNIKMasked)
	assert.Equal(t, "Alice", lines[0].AttendeeName)
	assert.Equal(t, int64(1000), lines[0].UnitPriceMinor)
	assert.Equal(t, "Carol", lines[1].AttendeeName)
	assert.Equal(t, int64(2000), uow.replaceSpec.NewAmountMinor)
}

func TestEdit_Keep_RejectsInvalid(t *testing.T) {
	tests := []struct {
		name  string
		items []dto.HoldItem
		is    func(error) bool
	}{
		{"unknown id", []dto.HoldItem{keepItem("aaaaaaaa-0000-0000-0000-0000000000ff")}, isValidation},
		{"non uuid", []dto.HoldItem{keepItem("not-a-uuid")}, isValidation},
		{"replaced item", []dto.HoldItem{keepItem(keepR)}, isValidation},
		{"released item", []dto.HoldItem{keepItem(keepX)}, isValidation},
		{"keep and nik", []dto.HoldItem{{CategoryID: catA, Attendee: dto.AttendeeInput{KeepFromItemID: keepA, NIK: nik2}}}, isValidation},
		{"keep and fullName", []dto.HoldItem{{CategoryID: catA, Attendee: dto.AttendeeInput{KeepFromItemID: keepA, FullName: "Zed"}}}, isValidation},
		{"empty attendee", []dto.HoldItem{{CategoryID: catA}}, isValidation},
		{"same keep twice", []dto.HoldItem{keepItem(keepA), keepItem(keepA)}, isDup},
		{"keep then new same nik", []dto.HoldItem{keepItem(keepA), newItem(nik1, "Dup")}, isDup},
		{"new then keep same nik", []dto.HoldItem{newItem(nik1, "Dup"), keepItem(keepA)}, isDup},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			uow := &fakeUoW{replaceVer: 2}
			uc := editUC(keepBooking(), activeAdmission(true), uow)
			_, err := uc.ReplaceItems(context.Background(), keepReq(tc.items...))
			require.Error(t, err)
			assert.True(t, tc.is(err), "got %v", err)
			assert.Equal(t, 0, uow.replaceCalls, "UoW must not be called")
		})
	}
}

func TestEdit_Keep_UnknownReasonsShareOneMessage(t *testing.T) {
	var msgs []string
	for _, id := range []string{"aaaaaaaa-0000-0000-0000-0000000000ff", "not-a-uuid", keepR, keepX} {
		uc := editUC(keepBooking(), activeAdmission(true), &fakeUoW{})
		_, err := uc.ReplaceItems(context.Background(), keepReq(keepItem(id)))
		require.Error(t, err)
		msgs = append(msgs, err.Error())
	}
	for _, m := range msgs[1:] {
		assert.Equal(t, msgs[0], m)
	}
}

func TestEdit_Keep_StillEnforcesGuards(t *testing.T) {
	started := time.Now()
	locked := keepBooking()
	locked.PaymentStartedAt = &started
	stale := keepBooking()
	stale.Version = 2
	limit := activeAdmission(true)
	limit.EditCount = 3
	tests := []struct {
		name string
		bk   entity.Booking
		adm  entity.Admission
		err  error
	}{
		{"locked", locked, activeAdmission(true), inbound.ErrBookingLocked},
		{"version", stale, activeAdmission(true), inbound.ErrVersionConflict},
		{"edit limit", keepBooking(), limit, inbound.ErrEditLimit},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			uow := &fakeUoW{}
			_, err := editUC(tc.bk, tc.adm, uow).ReplaceItems(context.Background(), keepReq(keepItem(keepA)))
			assert.ErrorIs(t, err, tc.err)
			assert.Equal(t, 0, uow.replaceCalls)
		})
	}
	t.Run("category unavailable", func(t *testing.T) {
		uow := &fakeUoW{}
		it := dto.HoldItem{CategoryID: "99999999-9999-9999-9999-999999999999", Attendee: dto.AttendeeInput{KeepFromItemID: keepA}}
		_, err := editUC(keepBooking(), activeAdmission(true), uow).ReplaceItems(context.Background(), keepReq(it))
		assert.ErrorIs(t, err, inbound.ErrCategoryUnavailable)
	})
}

func TestEdit_Keep_CategoryChangeAllowed(t *testing.T) {
	uow := &fakeUoW{replaceVer: 2}
	it := dto.HoldItem{CategoryID: catB, Attendee: dto.AttendeeInput{KeepFromItemID: keepA}}
	_, err := editUC(keepBooking(), activeAdmission(true), uow).ReplaceItems(context.Background(), keepReq(it))
	require.NoError(t, err)
	assert.Equal(t, catB, uow.replaceSpec.Lines[0].CategoryID)
}

func TestEdit_Keep_NoPlaintextNIKInSpec(t *testing.T) {
	uow := &fakeUoW{replaceVer: 2}
	_, err := editUC(keepBooking(), activeAdmission(true), uow).ReplaceItems(context.Background(), keepReq(keepItem(keepA)))
	require.NoError(t, err)
	l := uow.replaceSpec.Lines[0]
	assert.NotContains(t, l.AttendeeNIKMasked, nik1)
	assert.NotContains(t, l.AttendeeName, nik1)
}

func TestHold_RejectsKeepFromItemID(t *testing.T) {
	uow := &fakeUoW{}
	uc := newHoldUC(&fakeAdmissionRepo{admission: admission3()}, &fakeEventRepo{detail: detailWith(true, 3)}, &fakeIdem{}, uow)
	_, err := uc.Hold(context.Background(), holdCmd([]dto.HoldItem{
		{CategoryID: catA, Attendee: dto.AttendeeInput{KeepFromItemID: keepA}},
	}))
	require.Error(t, err)
	assert.True(t, isValidation(err), "got %v", err)
	assert.Empty(t, uow.heldSpec.Lines, "no stock must be held")
}

func isValidation(err error) bool {
	var ae *apperror.Error
	return errors.As(err, &ae) && ae.Code == inbound.CodeValidation && ae.HTTPStatus == 400
}

func isDup(err error) bool { return errors.Is(err, inbound.ErrNikDuplicate) }
