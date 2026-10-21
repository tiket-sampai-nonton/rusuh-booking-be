package usecase_test

import (
	"context"
	"time"

	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/booking/internal/booking/domain/dto"
	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/booking/internal/booking/domain/entity"
	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/booking/internal/booking/port/outbound"
)

// fakeNIK is a deterministic NIK protector for tests.
type fakeNIK struct{}

func (fakeNIK) HashNIK(nik string) []byte          { return []byte("h:" + nik) }
func (fakeNIK) SealNIK(nik string) ([]byte, error) { return []byte("e:" + nik), nil }

// fakeIDs returns predictable identifiers.
type fakeIDs struct {
	id  string
	ref string
	n   int
}

func (f *fakeIDs) NewID() string {
	f.n++
	if f.id != "" {
		return f.id
	}
	return "id-" + itoa(f.n)
}
func (f *fakeIDs) NewPublicRef() string {
	if f.ref != "" {
		return f.ref
	}
	return "RB-TEST"
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b []byte
	for n > 0 {
		b = append([]byte{byte('0' + n%10)}, b...)
		n /= 10
	}
	return string(b)
}

// fakeEventRepo serves a fixed event + detail.
type fakeEventRepo struct {
	detail entity.EventDetail
	event  entity.Event
	list   []entity.Event
	err    error
}

func (f *fakeEventRepo) ListEvents(context.Context, dto.EventListQuery) ([]entity.Event, error) {
	return f.list, f.err
}
func (f *fakeEventRepo) GetDetail(context.Context, string) (entity.EventDetail, error) {
	return f.detail, f.err
}
func (f *fakeEventRepo) GetByID(context.Context, string) (entity.Event, error) {
	if f.event.ID == "" && f.detail.Event.ID != "" {
		return f.detail.Event, f.err
	}
	return f.event, f.err
}

// fakeAdmissionRepo returns a fixed admission and records calls.
type fakeAdmissionRepo struct {
	admission  entity.Admission
	getErr     error
	activated  entity.Admission
	activErr   error
	reserve    []outbound.ReserveOutcome
	reserveErr error
	due        []entity.Admission
	expired    []string
}

func (f *fakeAdmissionRepo) FindByJTI(context.Context, string) (entity.Admission, error) {
	return f.admission, f.getErr
}
func (f *fakeAdmissionRepo) FindActiveByVisitor(context.Context, string, string) (entity.Admission, error) {
	return f.admission, f.getErr
}
func (f *fakeAdmissionRepo) FindActiveByEvent(context.Context, string, string) (entity.Admission, error) {
	return f.admission, f.getErr
}
func (f *fakeAdmissionRepo) GetByID(context.Context, string) (entity.Admission, error) {
	return f.admission, f.getErr
}
func (f *fakeAdmissionRepo) Activate(context.Context, string, time.Time) (entity.Admission, error) {
	return f.activated, f.activErr
}
func (f *fakeAdmissionRepo) ReserveBatch(context.Context, string, []outbound.ReserveEntry, time.Time) ([]outbound.ReserveOutcome, error) {
	return f.reserve, f.reserveErr
}
func (f *fakeAdmissionRepo) Release(context.Context, string) (bool, error) { return true, nil }
func (f *fakeAdmissionRepo) DueSessions(context.Context, time.Time, int) ([]entity.Admission, error) {
	return f.due, nil
}
func (f *fakeAdmissionRepo) ExpireSession(_ context.Context, id string, _ time.Time) error {
	f.expired = append(f.expired, id)
	return nil
}

// fakeIdem is a controllable idempotency store.
type fakeIdem struct {
	rec         outbound.IdempotencyRecord
	found       bool
	beginErr    error
	completed   bool
	completeErr error
}

func (f *fakeIdem) Begin(context.Context, string, string, string, []byte, time.Duration) (outbound.IdempotencyRecord, bool, error) {
	return f.rec, f.found, f.beginErr
}
func (f *fakeIdem) Complete(context.Context, string, string, string, int, []byte) error {
	f.completed = true
	return f.completeErr
}

// fakeUoW records specs and returns canned results.
type fakeUoW struct {
	holdRes     outbound.HoldResult
	holdErr     error
	heldSpec    outbound.HoldSpec
	replaceVer  int
	replaceErr  error
	settled     bool
	settleErr   error
	settleSpec  outbound.SettleSpec
	expireActed bool
	expireErr   error
	expireSpec  outbound.ExpireSpec
}

func (f *fakeUoW) Hold(_ context.Context, spec outbound.HoldSpec) (outbound.HoldResult, error) {
	f.heldSpec = spec
	return f.holdRes, f.holdErr
}
func (f *fakeUoW) ReplaceItems(context.Context, outbound.ReplaceSpec) (int, error) {
	return f.replaceVer, f.replaceErr
}
func (f *fakeUoW) Settle(_ context.Context, spec outbound.SettleSpec) (bool, error) {
	f.settleSpec = spec
	return f.settled, f.settleErr
}
func (f *fakeUoW) Expire(_ context.Context, spec outbound.ExpireSpec) (bool, error) {
	f.expireSpec = spec
	return f.expireActed, f.expireErr
}

// fakeBookingRepo returns a fixed booking.
type fakeBookingRepo struct {
	booking entity.Booking
	getErr  error
	list    []dto.MyBookingItem
	next    string
	listErr error
	due     []string
}

func (f *fakeBookingRepo) GetByID(context.Context, string) (entity.Booking, error) {
	return f.booking, f.getErr
}
func (f *fakeBookingRepo) ListByAccount(context.Context, string, string, int) ([]dto.MyBookingItem, string, error) {
	return f.list, f.next, f.listErr
}
func (f *fakeBookingRepo) DueBookingIDs(context.Context, time.Time, int) ([]string, error) {
	return f.due, nil
}

// fakeTicketRepo returns fixed tickets.
type fakeTicketRepo struct {
	tickets []entity.Ticket
	err     error
}

func (f *fakeTicketRepo) ListByBookingID(context.Context, string) ([]entity.Ticket, error) {
	return f.tickets, f.err
}

// fakeAttendeeChecker is controllable.
type fakeAttendeeChecker struct {
	registered bool
	regErr     error
	counted    bool
	recErr     error
}

func (f *fakeAttendeeChecker) IsRegistered(context.Context, string, []byte) (bool, error) {
	return f.registered, f.regErr
}
func (f *fakeAttendeeChecker) RecordCheck(context.Context, string, []byte) (bool, error) {
	return f.counted, f.recErr
}

// fakePayments resolves a canned result.
type fakePayments struct {
	paid bool
	err  error
}

func (f *fakePayments) Resolve(context.Context, string) (outbound.ResolveResult, error) {
	return outbound.ResolveResult{Paid: f.paid}, f.err
}

// fakeTokenVerifier returns canned admission claims.
type fakeTokenVerifier struct {
	claims outbound.AdmissionClaims
	err    error
}

func (f *fakeTokenVerifier) Verify(string) (outbound.AdmissionClaims, error) {
	return f.claims, f.err
}

// fixedClock implements clock.IClock.
type fixedClock struct{ t time.Time }

func (c fixedClock) Now() time.Time { return c.t }
