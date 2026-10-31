package usecase_test

import (
	"context"
	"encoding/hex"
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

func TestAllocateBatch_MapsOutcomes(t *testing.T) {
	adm := &fakeAdmissionRepo{reserve: []outbound.ReserveOutcome{
		{QueueEntryID: "q1", Outcome: "GRANTED", AllocationID: "a1", Units: 1},
		{QueueEntryID: "q2", Outcome: "ALREADY_ACTIVE"},
	}}
	uc := usecase.NewAllocateBatch(adm, &fakeIDs{}, fixedClock{t: time.Now()})
	resp, err := uc.AllocateBatch(context.Background(), dto.AllocationBatchRequest{
		EventID: eventID,
		Entries: []dto.AllocationEntry{
			{QueueEntryID: "q1", VisitorID: visitor, BuyerNIKHash: hex.EncodeToString([]byte("h")), Units: 1},
			{QueueEntryID: "q2", VisitorID: visitor, BuyerNIKHash: hex.EncodeToString([]byte("h")), Units: 1},
		},
	})
	require.NoError(t, err)
	assert.Len(t, resp.Results, 2)
	assert.Equal(t, "GRANTED", resp.Results[0].Outcome)
	assert.Equal(t, "ALREADY_ACTIVE", resp.Results[1].Outcome)
}

func TestAllocateBatch_BadEventID(t *testing.T) {
	uc := usecase.NewAllocateBatch(&fakeAdmissionRepo{}, &fakeIDs{}, fixedClock{t: time.Now()})
	_, err := uc.AllocateBatch(context.Background(), dto.AllocationBatchRequest{EventID: "nope", Entries: []dto.AllocationEntry{{QueueEntryID: "q"}}})
	assert.Error(t, err)
}

func TestCheckAttendee_InvalidNIKNoQuotaBurn(t *testing.T) {
	checker := &fakeAttendeeChecker{}
	uc := usecase.NewCheckAttendee(&fakeAdmissionRepo{admission: activeAdmission(true)}, &fakeEventRepo{event: entity.Event{ID: eventID, MaxAttendeeChecks: 10}}, checker, fakeNIK{}, fixedClock{t: time.Now()})
	_, err := uc.CheckAttendee(context.Background(), sessionID, dto.AttendeeCheckRequest{NIK: "bad"})
	assert.ErrorIs(t, err, inbound.ErrNIKInvalid)
}

func TestCheckAttendee_AvailableAndRegistered(t *testing.T) {
	adm := activeAdmission(true)
	adm.AttendeeChecks = 2
	ev := &fakeEventRepo{event: entity.Event{ID: eventID, MaxAttendeeChecks: 10}}

	uc := usecase.NewCheckAttendee(&fakeAdmissionRepo{admission: adm}, ev, &fakeAttendeeChecker{counted: true, registered: false}, fakeNIK{}, fixedClock{t: time.Now()})
	res, err := uc.CheckAttendee(context.Background(), sessionID, dto.AttendeeCheckRequest{NIK: nik1})
	require.NoError(t, err)
	assert.Equal(t, "AVAILABLE", res.Result)
	assert.Equal(t, 7, res.ChecksRemaining) // 10 - (2+1)

	uc2 := usecase.NewCheckAttendee(&fakeAdmissionRepo{admission: adm}, ev, &fakeAttendeeChecker{counted: false, registered: true}, fakeNIK{}, fixedClock{t: time.Now()})
	res2, err := uc2.CheckAttendee(context.Background(), sessionID, dto.AttendeeCheckRequest{NIK: nik1})
	require.NoError(t, err)
	assert.Equal(t, "ALREADY_REGISTERED", res2.Result)
}

func TestSettle_DelegatesToUoW(t *testing.T) {
	uow := &fakeUoW{settled: true}
	uc := usecase.NewSettle(uow, &fakeIDs{})
	err := uc.Settle(context.Background(), inbound.SettleCommand{BookingID: eventID, Method: "card"})
	require.NoError(t, err)
	assert.Equal(t, "card", uow.settleSpec.Method)
	assert.NotNil(t, uow.settleSpec.TicketCodeFn)
}

func TestSettle_PassesSettlementTime(t *testing.T) {
	uow := &fakeUoW{settled: true}
	uc := usecase.NewSettle(uow, &fakeIDs{})
	st := time.Date(2026, 10, 26, 9, 30, 0, 0, time.UTC)
	err := uc.Settle(context.Background(), inbound.SettleCommand{
		BookingID:      eventID,
		Method:         "VA_BCA",
		SettlementTime: st,
		CorrelationID:  "corr-1",
	})
	require.NoError(t, err)
	assert.Equal(t, st, uow.settleSpec.SettlementTime, "settlement time harus diteruskan ke UoW")
}

func TestExpire_NoPaymentExpires(t *testing.T) {
	// Phase A: PENDING_PAYMENT → StartVerifying (acted=true); Phase B: no payment attempt → Expire.
	verifyUntil := time.Now().Add(-1 * time.Second) // already past grace
	bkPending := entity.Booking{ID: eventID, Status: vo.BookingPendingPayment}
	bkVerifying := entity.Booking{ID: eventID, Status: vo.BookingVerifying, VerificationUntil: &verifyUntil}
	repo := &fakeBookingRepo{booking: bkPending, second: bkVerifying}
	uow := &fakeUoW{expireActed: true, startVerifyActed: true}
	uc := usecase.NewExpire(repo, &fakePayments{}, uow, 180, func(string) string { return "" }, nil)
	require.NoError(t, uc.ExpireBooking(context.Background(), eventID))
	assert.False(t, uow.expireSpec.Review)
	assert.Equal(t, "SESSION_EXPIRED", uow.expireSpec.Reason)
}

// Table of the Payment answers Booking must handle when a booking with a payment attempt expires.
func TestExpire_PaymentStarted_FollowsPaymentAnswer(t *testing.T) {
	deadline := time.Date(2026, 10, 26, 10, 0, 0, 0, time.UTC)
	started := deadline.Add(-5 * time.Minute)
	before, after := deadline.Add(-time.Second), deadline.Add(time.Second)
	verifyUntil := time.Now().UTC().Add(-1 * time.Second) // already past grace
	bk := entity.Booking{
		ID: eventID, Status: vo.BookingVerifying,
		PaymentStartedAt: &started, PaymentExpiresAt: deadline,
		VerificationUntil: &verifyUntil,
	}

	cases := map[string]struct {
		pay        *fakePayments
		wantErr    error
		wantSettle bool
		wantExpire bool
		wantReview bool
	}{
		"SUCCEEDED tepat waktu → PAID":                  {pay: &fakePayments{outcome: outbound.OutcomeSucceeded, settledAt: &before}, wantSettle: true},
		"SUCCEEDED tanpa waktu → PAID":                  {pay: &fakePayments{outcome: outbound.OutcomeSucceeded}, wantSettle: true},
		"SUCCEEDED terlambat → REVIEW":                  {pay: &fakePayments{outcome: outbound.OutcomeSucceeded, settledAt: &after}, wantExpire: true, wantReview: true},
		"NOT_PAID → EXPIRED":                            {pay: &fakePayments{outcome: outbound.OutcomeNotPaid}, wantExpire: true},
		"NO_ATTEMPT → EXPIRED":                          {pay: &fakePayments{outcome: outbound.OutcomeNoAttempt}, wantExpire: true},
		"UNKNOWN → stok tetap ditahan":                  {pay: &fakePayments{outcome: outbound.OutcomeUnknown}, wantErr: inbound.ErrDependency},
		"Payment tidak terjangkau → stok tetap ditahan": {pay: &fakePayments{err: inbound.ErrDependency}, wantErr: inbound.ErrDependency},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			uow := &fakeUoW{expireActed: true, settled: true}
			err := usecase.NewExpire(
				&fakeBookingRepo{booking: bk}, c.pay, uow, 180,
				func(string) string { return "TC-1" }, nil,
			).ExpireBooking(context.Background(), eventID)
			if c.wantErr != nil {
				assert.ErrorIs(t, err, c.wantErr)
			} else {
				require.NoError(t, err)
			}
			assert.Equal(t, c.wantSettle, uow.settleSpec.BookingID == eventID)
			assert.Equal(t, c.wantExpire, uow.expireSpec.BookingID == eventID)
			assert.Equal(t, c.wantReview, uow.expireSpec.Review)
		})
	}
}

func TestExpire_NoPaymentStartedNeverCallsPayment(t *testing.T) {
	pay := &fakePayments{}
	verifyUntil := time.Now().Add(-1 * time.Second)
	bkPending := entity.Booking{ID: eventID, Status: vo.BookingPendingPayment}
	bkVerifying := entity.Booking{ID: eventID, Status: vo.BookingVerifying, VerificationUntil: &verifyUntil}
	repo := &fakeBookingRepo{booking: bkPending, second: bkVerifying}
	require.NoError(t, usecase.NewExpire(repo, pay, &fakeUoW{expireActed: true, startVerifyActed: true}, 180, func(string) string { return "" }, nil).ExpireBooking(context.Background(), eventID))
	assert.Empty(t, pay.modes)
}

func TestExpire_AlreadyTerminalNoop(t *testing.T) {
	bk := entity.Booking{ID: eventID, Status: vo.BookingPaid}
	uow := &fakeUoW{}
	uc := usecase.NewExpire(&fakeBookingRepo{booking: bk}, &fakePayments{}, uow, 180, func(string) string { return "" }, nil)
	require.NoError(t, uc.ExpireBooking(context.Background(), eventID))
	assert.Empty(t, uow.expireSpec.BookingID)
}

func TestListEvents_Normalises(t *testing.T) {
	ev := &fakeEventRepo{list: []entity.Event{{ID: "e1"}}}
	uc := usecase.NewListEvents(ev, fixedClock{t: time.Now()})
	out, err := uc.ListEvents(context.Background(), dto.EventListQuery{Sort: "garbage"})
	require.NoError(t, err)
	assert.Len(t, out.Events, 1)
}

func TestGetEventDetail_InvalidUUID(t *testing.T) {
	uc := usecase.NewGetEventDetail(&fakeEventRepo{}, fixedClock{t: time.Now()})
	_, err := uc.GetEventDetail(context.Background(), "nope")
	assert.Error(t, err)
}

func TestGetEventDetail_OK(t *testing.T) {
	uc := usecase.NewGetEventDetail(&fakeEventRepo{detail: detailWith(true, 3)}, fixedClock{t: time.Now()})
	out, err := uc.GetEventDetail(context.Background(), eventID)
	require.NoError(t, err)
	assert.Len(t, out.Categories, 2)
}

func TestListMyBookings_Ownership(t *testing.T) {
	uc := usecase.NewListMyBookings(&fakeBookingRepo{list: []dto.MyBookingItem{{BookingID: "b1"}}, next: ""})
	_, err := uc.ListMyBookings(context.Background(), "", "", 0)
	assert.ErrorIs(t, err, inbound.ErrUnauthorized)

	out, err := uc.ListMyBookings(context.Background(), "acc-1", "", 0)
	require.NoError(t, err)
	assert.Len(t, out.Bookings, 1)
	assert.Nil(t, out.NextCursor)
}

func TestGetTickets_RequiresPaid(t *testing.T) {
	uc := usecase.NewGetTickets(&fakeBookingRepo{booking: entity.Booking{ID: eventID, Status: vo.BookingPendingPayment}}, &fakeTicketRepo{})
	_, err := uc.GetTickets(context.Background(), eventID)
	assert.ErrorIs(t, err, inbound.ErrNotPayable)

	uc2 := usecase.NewGetTickets(&fakeBookingRepo{booking: entity.Booking{ID: eventID, Status: vo.BookingPaid}}, &fakeTicketRepo{tickets: []entity.Ticket{{PublicCode: "TK-1"}}})
	out, err := uc2.GetTickets(context.Background(), eventID)
	require.NoError(t, err)
	assert.Len(t, out.Tickets, 1)
}

func TestSweepSessions_ReleasesDue(t *testing.T) {
	adm := &fakeAdmissionRepo{due: []entity.Admission{{ID: "a1"}, {ID: "a2"}}}
	uc := usecase.NewSweepSessions(adm, fixedClock{t: time.Now()})
	require.NoError(t, uc.SweepSessions(context.Background()))
	assert.Equal(t, []string{"a1", "a2"}, adm.expired)
}

type fakeExpiryUC struct{ ids []string }

func (f *fakeExpiryUC) ExpireBooking(_ context.Context, id string) error {
	f.ids = append(f.ids, id)
	return nil
}

func TestSweepExpiries_DelegatesEach(t *testing.T) {
	exp := &fakeExpiryUC{}
	uc := usecase.NewSweepExpiries(&fakeBookingRepo{due: []string{"b1", "b2"}}, exp, fixedClock{t: time.Now()})
	require.NoError(t, uc.SweepExpiries(context.Background()))
	assert.Equal(t, []string{"b1", "b2"}, exp.ids)
}
