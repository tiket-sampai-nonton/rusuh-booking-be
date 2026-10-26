package usecase

import (
	"context"

	"github.com/tiket-sampai-nonton/rusuh-booking-be/pkg/clock"
	pkgnik "github.com/tiket-sampai-nonton/rusuh-booking-be/pkg/nik"

	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/booking/internal/booking/domain/dto"
	vo "github.com/tiket-sampai-nonton/rusuh-booking-be/services/booking/internal/booking/domain/valueobject"
	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/booking/internal/booking/port/inbound"
	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/booking/internal/booking/port/outbound"
)

// Attendee-check results.
const (
	checkAvailable  = "AVAILABLE"
	checkRegistered = "ALREADY_REGISTERED"
	checkLimit      = "CHECK_LIMIT_REACHED"
)

// CheckAttendee probes an attendee NIK during selection: validate structure → dedupe within session →
// active-registration probe → return the result and remaining quota.
type CheckAttendee struct {
	admissions outbound.IAdmissionRepo
	events     outbound.IEventRepo
	checker    outbound.IAttendeeChecker
	nik        vo.NIKProtector
	clock      clock.IClock
}

// NewCheckAttendee wires the attendee-check usecase.
func NewCheckAttendee(admissions outbound.IAdmissionRepo, events outbound.IEventRepo,
	checker outbound.IAttendeeChecker, nik vo.NIKProtector, clk clock.IClock) *CheckAttendee {
	return &CheckAttendee{admissions: admissions, events: events, checker: checker, nik: nik, clock: clk}
}

// CheckAttendee runs the probe. A structurally invalid NIK returns NIK_INVALID (422) WITHOUT consuming
// the check quota (AC5.2).
func (uc *CheckAttendee) CheckAttendee(ctx context.Context, sessionID string, req dto.AttendeeCheckRequest) (dto.AttendeeCheck, error) {
	if !isUUID(sessionID) {
		return dto.AttendeeCheck{}, inbound.Validation("sessionId", "sessionId harus UUID")
	}
	if err := pkgnik.Validate(req.NIK, uc.clock.Now()); err != nil {
		return dto.AttendeeCheck{}, inbound.ErrNIKInvalid // no quota decrement
	}
	admission, err := uc.admissions.GetByID(ctx, sessionID)
	if err != nil {
		return dto.AttendeeCheck{}, err
	}
	if admission.IsExpired(uc.clock.Now()) || !admission.IsActive() {
		return dto.AttendeeCheck{}, inbound.ErrSessionExpired
	}
	event, err := uc.events.GetByID(ctx, admission.EventID)
	if err != nil {
		return dto.AttendeeCheck{}, err
	}
	maxChecks := event.MaxAttendeeChecks

	nik, err := vo.ParseNIKHashOnly(req.NIK, uc.nik)
	if err != nil {
		return dto.AttendeeCheck{}, inbound.ErrNIKInvalid
	}

	// Dedupe: re-checking the same NIK does not count again.
	counted, err := uc.checker.RecordCheck(ctx, admission.ID, nik.Hash())
	if err != nil {
		return dto.AttendeeCheck{}, err
	}
	used := admission.AttendeeChecks
	if counted {
		used++
	}
	remaining := maxChecks - used
	if remaining < 0 {
		remaining = 0
	}
	if counted && used > maxChecks {
		return dto.AttendeeCheck{Result: checkLimit, ChecksRemaining: 0}, nil
	}

	registered, err := uc.checker.IsRegistered(ctx, admission.EventID, nik.Hash())
	if err != nil {
		return dto.AttendeeCheck{}, err
	}
	result := checkAvailable
	if registered {
		result = checkRegistered
	}
	return dto.AttendeeCheck{Result: result, ChecksRemaining: remaining}, nil
}
