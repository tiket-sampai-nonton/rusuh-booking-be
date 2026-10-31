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

// boolPtr returns a pointer to a bool value (used in tests with releaseResult).
func boolPtr(b bool) *bool { return &b }

// fakeAdmissionRepo returns a fixed admission and records calls.
type fakeAdmissionRepo struct {
	admission     entity.Admission
	getErr        error
	activated     entity.Admission
	activErr      error
	reserve       []outbound.ReserveOutcome
	reserveErr    error
	due           []entity.Admission
	expired       []string
	released      []string
	releaseResult *bool // nil = default true
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
func (f *fakeAdmissionRepo) Release(_ context.Context, id string) (bool, error) {
	if f.getErr != nil {
		return false, f.getErr
	}
	f.released = append(f.released, id)
	if f.releaseResult != nil {
		return *f.releaseResult, nil
	}
	return true, nil
}
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
	holdRes          outbound.HoldResult
	holdErr          error
	heldSpec         outbound.HoldSpec
	replaceVer       int
	replaceErr       error
	settled          bool
	settleErr        error
	settleSpec       outbound.SettleSpec
	expireActed      bool
	expireErr        error
	expireSpec       outbound.ExpireSpec
	extendErr        error
	extendCalls      int
	lockResult       outbound.PaymentLockResult
	lockErr          error
	lockCalls        int
	startVerifyActed bool
	startVerifyErr   error
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
func (f *fakeUoW) ExtendDeadline(_ context.Context, _ outbound.DeadlineSpec) error {
	f.extendCalls++
	return f.extendErr
}
func (f *fakeUoW) PaymentLock(_ context.Context, _ outbound.PaymentLockSpec) (outbound.PaymentLockResult, error) {
	f.lockCalls++
	return f.lockResult, f.lockErr
}
func (f *fakeUoW) StartVerifying(_ context.Context, _ string, _ int) (bool, error) {
	return f.startVerifyActed, f.startVerifyErr
}

// fakeBookingRepo returns a fixed booking (second booking returned on second call if set).
type fakeBookingRepo struct {
	booking entity.Booking
	second  entity.Booking // returned on the 2nd GetByID call (for two-phase expire tests)
	calls   int
	getErr  error
	list    []dto.MyBookingItem
	next    string
	listErr error
	due     []string
}

func (f *fakeBookingRepo) GetByID(context.Context, string) (entity.Booking, error) {
	f.calls++
	if f.calls > 1 && f.second.ID != "" {
		return f.second, f.getErr
	}
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

// fakePayments resolves a canned result; the zero value answers NOT_PAID. It records the modes it was asked with.
type fakePayments struct {
	outcome   outbound.ResolveOutcome
	settledAt *time.Time
	err       error
	modes     []outbound.ResolveMode
}

func (f *fakePayments) Resolve(_ context.Context, _ string, mode outbound.ResolveMode) (outbound.ResolveResult, error) {
	f.modes = append(f.modes, mode)
	if f.err != nil {
		return outbound.ResolveResult{}, f.err
	}
	o := f.outcome
	if o == "" {
		o = outbound.OutcomeNotPaid
	}
	return outbound.ResolveResult{Outcome: o, SettlementTime: f.settledAt}, nil
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

// fakeAvailCache is a controllable IAvailabilityCache for SSE tests.
type fakeAvailCache struct {
	snaps   []outbound.CategoryAvailabilitySnap
	snapErr error
	setErr  error
	written map[string][]outbound.CategoryAvailabilitySnap
	connN   int
	connErr error
}

func (f *fakeAvailCache) GetAvailability(_ context.Context, _ string) ([]outbound.CategoryAvailabilitySnap, error) {
	return f.snaps, f.snapErr
}
func (f *fakeAvailCache) SetAvailability(_ context.Context, id string, snaps []outbound.CategoryAvailabilitySnap, _ time.Duration) error {
	if f.setErr != nil {
		return f.setErr
	}
	if f.written == nil {
		f.written = map[string][]outbound.CategoryAvailabilitySnap{}
	}
	f.written[id] = snaps
	return nil
}
func (f *fakeAvailCache) IncrConn(_ context.Context, _ string) (int, error) {
	return f.connN, f.connErr
}
func (f *fakeAvailCache) DecrConn(_ context.Context, _ string) error { return nil }

// fakeEventWriteRepo records admin event writes and returns canned errors.
type fakeEventWriteRepo struct {
	createErr error
	updateErr error
	imageErr  error
	imageKind string
	imageURL  string
}

func (f *fakeEventWriteRepo) SetImageURL(_ context.Context, _, kind, url string) error {
	f.imageKind, f.imageURL = kind, url
	return f.imageErr
}

func (f *fakeEventWriteRepo) CreateEvent(_ context.Context, _ string, _ dto.CreateEventRequest) error {
	return f.createErr
}
func (f *fakeEventWriteRepo) UpdateConfig(_ context.Context, _ string, _ dto.UpdateEventConfigRequest) error {
	return f.updateErr
}
