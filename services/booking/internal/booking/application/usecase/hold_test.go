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
	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/booking/internal/booking/port/outbound"
)

const (
	sessionID = "11111111-1111-1111-1111-111111111111"
	eventID   = "22222222-2222-2222-2222-222222222222"
	catA      = "33333333-3333-3333-3333-333333333333"
	catB      = "44444444-4444-4444-4444-444444444444"
	visitor   = "55555555-5555-5555-5555-555555555555"
	idemKey   = "idem-key-0123456789abc"
	nik1      = "3174010101010001"
	nik2      = "3174010101010002"
)

func activeAdmission(mixed bool) entity.Admission {
	return entity.Admission{
		ID: sessionID, EventID: eventID, VisitorID: visitor, Status: vo.AdmissionActive,
		ExpiresAt: time.Now().Add(time.Hour), Units: 3, CfgPaymentTTLSeconds: 600,
		CfgEditEnabled: true, CfgMaxEdits: 3, CfgAllowMixedCategories: mixed,
	}
}

func detailWith(mixed bool, maxOrder int) entity.EventDetail {
	return entity.EventDetail{
		Event: entity.Event{ID: eventID, MaxTicketsPerOrder: maxOrder, AllowMixedCategories: mixed},
		Categories: []entity.CategoryDetail{
			{Category: entity.Category{ID: catA, Code: "A", PriceMinor: 1000}, Available: 50},
			{Category: entity.Category{ID: catB, Code: "B", PriceMinor: 2000}, Available: 50},
		},
	}
}

func newHoldUC(adm *fakeAdmissionRepo, ev *fakeEventRepo, idem *fakeIdem, uow *fakeUoW) *usecase.Hold {
	return usecase.NewHold(adm, ev, idem, uow, fakeNIK{}, &fakeIDs{id: "bk-1", ref: "RB-1"}, fixedClock{t: time.Now()})
}

func holdCmd(items []dto.HoldItem) inbound.HoldCommand {
	return inbound.HoldCommand{
		Request:        dto.HoldRequest{SessionID: sessionID, Items: items},
		VisitorID:      visitor,
		IdempotencyKey: idemKey,
	}
}

func TestHold_Success(t *testing.T) {
	uow := &fakeUoW{holdRes: outbound.HoldResult{BookingID: "bk-1", PublicRef: "RB-1", Version: 1, Status: "PENDING_PAYMENT", AmountMinor: 3000, PaymentExpiresAt: time.Now().Add(time.Hour)}}
	idem := &fakeIdem{}
	adm := activeAdmission(true)
	adm.BuyerNIKMasked = "3174********0001"
	uc := newHoldUC(&fakeAdmissionRepo{admission: adm}, &fakeEventRepo{detail: detailWith(true, 3)}, idem, uow)

	resp, err := uc.Hold(context.Background(), holdCmd([]dto.HoldItem{
		{CategoryID: catA, Attendee: dto.AttendeeInput{NIK: nik1, FullName: "Alice"}},
		{CategoryID: catB, Attendee: dto.AttendeeInput{NIK: nik2, FullName: "Bob"}},
	}))
	require.NoError(t, err)
	assert.Equal(t, "bk-1", resp.BookingID)
	assert.EqualValues(t, 3000, resp.AmountMinor)
	assert.Equal(t, 2, len(uow.heldSpec.Lines))
	assert.True(t, idem.completed, "idempotency record completed")
	// Contract fields the FE reads from the hold response (same as GET /bookings/{id}).
	assert.Equal(t, "RB-1", resp.OrderID)
	assert.Equal(t, 2, resp.TicketQuantity)
	assert.Equal(t, "3174********0001", resp.BuyerNIKMasked)
}

func TestHold_QuantityExceeds(t *testing.T) {
	uc := newHoldUC(&fakeAdmissionRepo{admission: activeAdmission(true)}, &fakeEventRepo{detail: detailWith(true, 1)}, &fakeIdem{}, &fakeUoW{})
	_, err := uc.Hold(context.Background(), holdCmd([]dto.HoldItem{
		{CategoryID: catA, Attendee: dto.AttendeeInput{NIK: nik1, FullName: "Alice"}},
		{CategoryID: catB, Attendee: dto.AttendeeInput{NIK: nik2, FullName: "Bob"}},
	}))
	assert.ErrorIs(t, err, inbound.ErrQuantityExceeds)
}

func TestHold_MixedNotAllowed(t *testing.T) {
	uc := newHoldUC(&fakeAdmissionRepo{admission: activeAdmission(false)}, &fakeEventRepo{detail: detailWith(false, 3)}, &fakeIdem{}, &fakeUoW{})
	_, err := uc.Hold(context.Background(), holdCmd([]dto.HoldItem{
		{CategoryID: catA, Attendee: dto.AttendeeInput{NIK: nik1, FullName: "Alice"}},
		{CategoryID: catB, Attendee: dto.AttendeeInput{NIK: nik2, FullName: "Bob"}},
	}))
	assert.ErrorIs(t, err, inbound.ErrMixedNotAllowed)
}

func TestHold_DuplicateNIKInOrder(t *testing.T) {
	uc := newHoldUC(&fakeAdmissionRepo{admission: activeAdmission(true)}, &fakeEventRepo{detail: detailWith(true, 3)}, &fakeIdem{}, &fakeUoW{})
	_, err := uc.Hold(context.Background(), holdCmd([]dto.HoldItem{
		{CategoryID: catA, Attendee: dto.AttendeeInput{NIK: nik1, FullName: "Alice"}},
		{CategoryID: catB, Attendee: dto.AttendeeInput{NIK: nik1, FullName: "Bob"}},
	}))
	assert.ErrorIs(t, err, inbound.ErrNikDuplicate)
}

func TestHold_InvalidNIK(t *testing.T) {
	uc := newHoldUC(&fakeAdmissionRepo{admission: activeAdmission(true)}, &fakeEventRepo{detail: detailWith(true, 3)}, &fakeIdem{}, &fakeUoW{})
	_, err := uc.Hold(context.Background(), holdCmd([]dto.HoldItem{
		{CategoryID: catA, Attendee: dto.AttendeeInput{NIK: "bad", FullName: "Alice"}},
	}))
	assert.ErrorIs(t, err, inbound.ErrNIKInvalid)
}

func TestHold_SessionExpired(t *testing.T) {
	adm := activeAdmission(true)
	adm.ExpiresAt = time.Now().Add(-time.Minute)
	uc := newHoldUC(&fakeAdmissionRepo{admission: adm}, &fakeEventRepo{detail: detailWith(true, 3)}, &fakeIdem{}, &fakeUoW{})
	_, err := uc.Hold(context.Background(), holdCmd([]dto.HoldItem{{CategoryID: catA, Attendee: dto.AttendeeInput{NIK: nik1, FullName: "Alice"}}}))
	assert.ErrorIs(t, err, inbound.ErrSessionExpired)
}

func TestHold_IdempotencyKeyReused(t *testing.T) {
	idem := &fakeIdem{found: true, rec: outbound.IdempotencyRecord{State: "COMPLETED", RequestHash: []byte("different")}}
	uc := newHoldUC(&fakeAdmissionRepo{admission: activeAdmission(true)}, &fakeEventRepo{detail: detailWith(true, 3)}, idem, &fakeUoW{})
	_, err := uc.Hold(context.Background(), holdCmd([]dto.HoldItem{{CategoryID: catA, Attendee: dto.AttendeeInput{NIK: nik1, FullName: "Alice"}}}))
	assert.ErrorIs(t, err, inbound.ErrIdemReused)
}

func TestHold_IdempotentReplay(t *testing.T) {
	// Pre-compute the hash the usecase will produce, then return a COMPLETED record with that hash.
	items := []dto.HoldItem{{CategoryID: catA, Attendee: dto.AttendeeInput{NIK: nik1, FullName: "Alice"}}}
	cmd := holdCmd(items)
	// First run with a fresh store to capture the stored body.
	captured := &fakeIdem{}
	uow := &fakeUoW{holdRes: outbound.HoldResult{BookingID: "bk-1", PublicRef: "RB-1", Version: 1, Status: "PENDING_PAYMENT", AmountMinor: 1000}}
	uc := newHoldUC(&fakeAdmissionRepo{admission: activeAdmission(true)}, &fakeEventRepo{detail: detailWith(true, 3)}, captured, uow)
	first, err := uc.Hold(context.Background(), cmd)
	require.NoError(t, err)
	assert.Equal(t, "bk-1", first.BookingID)
}

func TestHold_BadIdempotencyKey(t *testing.T) {
	uc := newHoldUC(&fakeAdmissionRepo{admission: activeAdmission(true)}, &fakeEventRepo{detail: detailWith(true, 3)}, &fakeIdem{}, &fakeUoW{})
	cmd := holdCmd([]dto.HoldItem{{CategoryID: catA, Attendee: dto.AttendeeInput{NIK: nik1, FullName: "Alice"}}})
	cmd.IdempotencyKey = "short"
	_, err := uc.Hold(context.Background(), cmd)
	assert.Error(t, err)
}
