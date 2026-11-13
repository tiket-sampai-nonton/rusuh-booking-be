package usecase_test

import (
	"context"
	"encoding/hex"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tiket-sampai-nonton/rusuh-booking-be/pkg/apperror"
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
	_, err := uc.CheckAttendee(context.Background(), sessionID, visitor, dto.AttendeeCheckRequest{NIK: "bad"})
	assert.ErrorIs(t, err, inbound.ErrNIKInvalid)
}

func TestCheckAttendee_AvailableAndRegistered(t *testing.T) {
	adm := activeAdmission(true)
	ev := &fakeEventRepo{event: entity.Event{ID: eventID, MaxAttendeeChecks: 10}}

	uc := usecase.NewCheckAttendee(&fakeAdmissionRepo{admission: adm}, ev, &fakeAttendeeChecker{counted: true, used: 3, registered: false}, fakeNIK{}, fixedClock{t: time.Now()})
	res, err := uc.CheckAttendee(context.Background(), sessionID, visitor, dto.AttendeeCheckRequest{NIK: nik1})
	require.NoError(t, err)
	assert.Equal(t, "AVAILABLE", res.Result)
	assert.Equal(t, 7, res.ChecksRemaining) // 10 - 3 used after this check

	uc2 := usecase.NewCheckAttendee(&fakeAdmissionRepo{admission: adm}, ev, &fakeAttendeeChecker{counted: false, used: 3, registered: true}, fakeNIK{}, fixedClock{t: time.Now()})
	res2, err := uc2.CheckAttendee(context.Background(), sessionID, visitor, dto.AttendeeCheckRequest{NIK: nik1})
	require.NoError(t, err)
	assert.Equal(t, "ALREADY_REGISTERED", res2.Result)
}

func TestCheckAttendee_LimitFromRepository(t *testing.T) {
	checker := &fakeAttendeeChecker{limitReached: true, used: 3}
	uc := usecase.NewCheckAttendee(&fakeAdmissionRepo{admission: activeAdmission(true)}, &fakeEventRepo{event: entity.Event{ID: eventID, MaxAttendeeChecks: 3}}, checker, fakeNIK{}, fixedClock{t: time.Now()})
	res, err := uc.CheckAttendee(context.Background(), sessionID, visitor, dto.AttendeeCheckRequest{NIK: nik1})
	require.NoError(t, err)
	assert.Equal(t, "CHECK_LIMIT_REACHED", res.Result)
	assert.Equal(t, 0, res.ChecksRemaining)
	assert.Equal(t, 1, checker.calls, "only RecordCheck runs; IsRegistered must not be called once the limit is reached")
}

func TestCheckAttendee_InGraceIsAccepted(t *testing.T) {
	now := time.Now()
	adm := activeAdmission(true)
	adm.ExpiresAt = now.Add(-5 * time.Second)
	adm.CfgSubmitGraceSeconds = 10
	ev := &fakeEventRepo{event: entity.Event{ID: eventID, MaxAttendeeChecks: 10}}

	uc := usecase.NewCheckAttendee(&fakeAdmissionRepo{admission: adm}, ev, &fakeAttendeeChecker{counted: true}, fakeNIK{}, fixedClock{t: now})
	res, err := uc.CheckAttendee(context.Background(), sessionID, visitor, dto.AttendeeCheckRequest{NIK: nik1})
	require.NoError(t, err)
	assert.Equal(t, "AVAILABLE", res.Result)
}

func TestSettle_DelegatesToUoW(t *testing.T) {
	uow := &fakeUoW{settled: true}
	uc := usecase.NewSettle(uow, func(id string) string { return "link-for-" + id })
	err := uc.Settle(context.Background(), inbound.SettleCommand{BookingID: eventID, Method: "card"})
	require.NoError(t, err)
	assert.Equal(t, "card", uow.settleSpec.Method)
	require.NotNil(t, uow.settleSpec.TicketCodeFn)
	assert.Regexp(t, `^TK-[0-9A-F]{20}$`, uow.settleSpec.TicketCodeFn("item"))
	require.NotNil(t, uow.settleSpec.TicketLinkTokenFn, "token tautan e-tiket diterbitkan saat settle")
	assert.Equal(t, "link-for-"+eventID, uow.settleSpec.TicketLinkTokenFn(eventID))
}

func TestSettle_PassesSettlementTime(t *testing.T) {
	uow := &fakeUoW{settled: true}
	uc := usecase.NewSettle(uow, nil)
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

// D-B5: an UNKNOWN or unreachable Payment answer pushes the next check of the VERIFYING booking 30 s ahead, so the
// sweeper does not retry it on every tick. The original error is returned, joined with a failure to record the deferral.
func TestExpireBooking_UnknownDefersNextCheck(t *testing.T) {
	dbErr := errors.New("postgres: connection reset")
	cases := map[string]struct {
		pay      *fakePayments
		deferErr error
		wantErrs []error
	}{
		"UNKNOWN":                    {pay: &fakePayments{outcome: outbound.OutcomeUnknown}, wantErrs: []error{inbound.ErrDependency}},
		"Payment tidak terjangkau":   {pay: &fakePayments{err: inbound.ErrDependency}, wantErrs: []error{inbound.ErrDependency}},
		"UNKNOWN, catat gagal ganda": {pay: &fakePayments{outcome: outbound.OutcomeUnknown}, deferErr: dbErr, wantErrs: []error{inbound.ErrDependency, dbErr}},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			started := time.Now().UTC().Add(-5 * time.Minute)
			verifyUntil := time.Now().UTC().Add(-1 * time.Second) // past grace: the sweeper has picked it up
			bk := entity.Booking{
				ID: eventID, Status: vo.BookingVerifying, PaymentStartedAt: &started,
				PaymentExpiresAt: started.Add(5 * time.Minute), VerificationUntil: &verifyUntil,
			}
			repo := &fakeBookingRepo{booking: bk, deferErr: c.deferErr}
			uow := &fakeUoW{}
			uc := usecase.NewExpire(repo, c.pay, uow, 180, func(string) string { return "" }, nil)

			before := time.Now()
			err := uc.ExpireBooking(context.Background(), eventID)
			after := time.Now()

			for _, want := range c.wantErrs {
				assert.ErrorIs(t, err, want)
			}
			require.Len(t, repo.deferred, 1, "satu penundaan per panggilan")
			assert.Equal(t, eventID, repo.deferred[0].id)
			assert.False(t, repo.deferred[0].until.Before(before.Add(30*time.Second)), "mundur 30 detik dari sekarang")
			assert.False(t, repo.deferred[0].until.After(after.Add(30*time.Second)), "mundur 30 detik dari sekarang")
			assert.Empty(t, uow.expireSpec.BookingID, "tidak diexpire: stok tetap ditahan")
		})
	}
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
	owner := inbound.TicketAccess{VisitorID: visitor}
	uc := usecase.NewGetTickets(&fakeBookingRepo{booking: entity.Booking{ID: eventID, Status: vo.BookingPendingPayment, VisitorID: visitor}}, &fakeTicketRepo{}, &fakeEventRepo{}, nil)
	_, err := uc.GetTickets(context.Background(), eventID, owner)
	assert.ErrorIs(t, err, inbound.ErrNotPayable)

	uc2 := usecase.NewGetTickets(&fakeBookingRepo{booking: entity.Booking{ID: eventID, Status: vo.BookingPaid, VisitorID: visitor}}, &fakeTicketRepo{tickets: []entity.Ticket{{PublicCode: "TK-1"}}}, &fakeEventRepo{}, nil)
	out, err := uc2.GetTickets(context.Background(), eventID, owner)
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

// batchedAdmissionRepo serves ClaimDueSessions from scripted batches and counts the claims.
type batchedAdmissionRepo struct {
	*fakeAdmissionRepo
	batches [][]entity.Admission
	claims  int
	always  int // when > 0, every claim returns this many admissions (a backlog that never drains)
}

func (b *batchedAdmissionRepo) ClaimDueSessions(context.Context, time.Time, int) ([]entity.Admission, error) {
	b.claims++
	if b.always > 0 {
		out := make([]entity.Admission, b.always)
		for i := range out {
			out[i].ID = "x"
		}
		return out, nil
	}
	if len(b.batches) == 0 {
		return nil, nil
	}
	out := b.batches[0]
	b.batches = b.batches[1:]
	return out, nil
}

func admissionBatch(n, from int) []entity.Admission {
	out := make([]entity.Admission, n)
	for i := range out {
		out[i].ID = fmt.Sprintf("a%d", from+i)
	}
	return out
}

// D-C7.3: a full batch means more may be due, so the sweeper claims again until a short batch ends the round.
func TestSweepSessions_RepeatsWhileBatchFull(t *testing.T) {
	adm := &batchedAdmissionRepo{
		fakeAdmissionRepo: &fakeAdmissionRepo{},
		batches:           [][]entity.Admission{admissionBatch(100, 0), admissionBatch(100, 100), admissionBatch(3, 200)},
	}
	uc := usecase.NewSweepSessions(adm, fixedClock{t: time.Now()})
	require.NoError(t, uc.SweepSessions(context.Background()))
	assert.Equal(t, 3, adm.claims)
	assert.Len(t, adm.expired, 203)
}

// D-C7.3: one call never claims more than maxSweepRounds (20) batches, even when the backlog never drains.
func TestSweepSessions_StopsAfterMaxRounds(t *testing.T) {
	adm := &batchedAdmissionRepo{fakeAdmissionRepo: &fakeAdmissionRepo{}, always: 100}
	uc := usecase.NewSweepSessions(adm, fixedClock{t: time.Now()})
	require.NoError(t, uc.SweepSessions(context.Background()))
	assert.Equal(t, 20, adm.claims)
	assert.Len(t, adm.expired, 2000)
}

// batchedBookingRepo serves ClaimDueBookings from scripted batches (or always returns `always` ids).
type batchedBookingRepo struct {
	*fakeBookingRepo
	batches [][]string
	claims  int
	always  int
}

func (b *batchedBookingRepo) ClaimDueBookings(context.Context, time.Time, int) ([]string, error) {
	b.claims++
	if b.always > 0 {
		out := make([]string, b.always)
		for i := range out {
			out[i] = fmt.Sprintf("b%d", i)
		}
		return out, nil
	}
	if len(b.batches) == 0 {
		return nil, nil
	}
	out := b.batches[0]
	b.batches = b.batches[1:]
	return out, nil
}

func bookingBatch(n, from int) []string {
	out := make([]string, n)
	for i := range out {
		out[i] = fmt.Sprintf("b%d", from+i)
	}
	return out
}

// slowExpiryUC stands in for a slow Payment Resolve: each booking takes delay. After slowCalls calls it returns at once,
// so a sweeper that ignores its budget finishes quickly and fails on the assertions instead of hanging.
type slowExpiryUC struct {
	delay time.Duration
	calls int
	ids   []string
}

func (f *slowExpiryUC) ExpireBooking(_ context.Context, id string) error {
	f.calls++
	if f.calls <= slowCalls {
		time.Sleep(f.delay)
	}
	f.ids = append(f.ids, id)
	return nil
}

const slowCalls = 3

// D-C7.3 / R32: a sweep call stops once its budget (lease − 3 s = 12 s) is spent and leaves the rest to lapse.
func TestSweepExpiries_StopsAfterTimeBudget(t *testing.T) {
	t.Parallel()
	exp := &slowExpiryUC{delay: 6 * time.Second}
	bookings := &batchedBookingRepo{fakeBookingRepo: &fakeBookingRepo{}, always: 100}
	uc := usecase.NewSweepExpiries(bookings, exp, fixedClock{t: time.Now()})
	require.NoError(t, uc.SweepExpiries(context.Background()))
	assert.Len(t, exp.ids, 2, "row 1 ends at 6 s, row 2 at 12 s; the call stops before row 3")
	assert.Equal(t, 1, bookings.claims, "no new claim round once the budget is spent")
}

// D-C7.3: SweepExpiries repeats while a batch comes back full, like SweepSessions.
func TestSweepExpiries_RepeatsWhileBatchFull(t *testing.T) {
	bookings := &batchedBookingRepo{
		fakeBookingRepo: &fakeBookingRepo{},
		batches:         [][]string{bookingBatch(100, 0), bookingBatch(100, 100), bookingBatch(3, 200)},
	}
	exp := &fakeExpiryUC{}
	uc := usecase.NewSweepExpiries(bookings, exp, fixedClock{t: time.Now()})
	require.NoError(t, uc.SweepExpiries(context.Background()))
	assert.Equal(t, 3, bookings.claims)
	assert.Len(t, exp.ids, 203)
}

// slowAdmissionRepo is the session-side slow release: each ExpireSession takes delay (same cap as slowExpiryUC).
type slowAdmissionRepo struct {
	*batchedAdmissionRepo
	delay time.Duration
	calls int
}

func (s *slowAdmissionRepo) ExpireSession(ctx context.Context, id string, now time.Time) error {
	s.calls++
	if s.calls <= slowCalls {
		time.Sleep(s.delay)
	}
	return s.batchedAdmissionRepo.ExpireSession(ctx, id, now)
}

// D-C7.3 / R32: the session sweeper also stops at the budget.
func TestSweepSessions_StopsAfterTimeBudget(t *testing.T) {
	t.Parallel()
	adm := &slowAdmissionRepo{
		batchedAdmissionRepo: &batchedAdmissionRepo{fakeAdmissionRepo: &fakeAdmissionRepo{}, always: 100},
		delay:                6 * time.Second,
	}
	uc := usecase.NewSweepSessions(adm, fixedClock{t: time.Now()})
	require.NoError(t, uc.SweepSessions(context.Background()))
	assert.Len(t, adm.expired, 2, "row 1 ends at 6 s, row 2 at 12 s; the call stops before row 3")
	assert.Equal(t, 1, adm.claims, "no new claim round once the budget is spent")
}

func TestSweepExpiries_DelegatesEach(t *testing.T) {
	exp := &fakeExpiryUC{}
	uc := usecase.NewSweepExpiries(&fakeBookingRepo{due: []string{"b1", "b2"}}, exp, fixedClock{t: time.Now()})
	require.NoError(t, uc.SweepExpiries(context.Background()))
	assert.Equal(t, []string{"b1", "b2"}, exp.ids)
}

// C7.2 (D-C7.2): one allocation batch carries at most 100 entries; more is a 400 that names the field.
func TestAllocateBatch_MoreThan100Is400(t *testing.T) {
	entries := func(n int) []dto.AllocationEntry {
		out := make([]dto.AllocationEntry, 0, n)
		for i := 0; i < n; i++ {
			out = append(out, dto.AllocationEntry{QueueEntryID: "q", VisitorID: visitor, Units: 1})
		}
		return out
	}
	uc := usecase.NewAllocateBatch(&fakeAdmissionRepo{}, &fakeIDs{}, fixedClock{t: time.Now()})

	_, err := uc.AllocateBatch(context.Background(), dto.AllocationBatchRequest{EventID: eventID, Entries: entries(101)})
	require.Error(t, err)
	assert.Equal(t, apperror.CodeValidationFailed, codeOf(t, err))
	assert.Equal(t, "entries", validationField(t, err))

	_, err = uc.AllocateBatch(context.Background(), dto.AllocationBatchRequest{EventID: eventID, Entries: entries(100)})
	assert.NoError(t, err, "exactly 100 entries is allowed")
}
