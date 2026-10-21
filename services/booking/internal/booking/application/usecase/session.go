package usecase

import (
	"context"

	"github.com/tiket-sampai-nonton/rusuh-booking-be/pkg/clock"

	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/booking/internal/booking/domain/dto"
	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/booking/internal/booking/domain/entity"
	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/booking/internal/booking/domain/mapper"
	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/booking/internal/booking/port/inbound"
	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/booking/internal/booking/port/outbound"
)

// Session opens and reads booking sessions.
type Session struct {
	admissions outbound.IAdmissionRepo
	events     outbound.IEventRepo
	verifier   outbound.IAdmissionTokenVerifier
	clock      clock.IClock
}

// NewSession wires the session usecase.
func NewSession(admissions outbound.IAdmissionRepo, events outbound.IEventRepo,
	verifier outbound.IAdmissionTokenVerifier, clk clock.IClock) *Session {
	return &Session{admissions: admissions, events: events, verifier: verifier, clock: clk}
}

// CreateSession verifies the one-time admission token and flips RESERVED → ACTIVE.
func (uc *Session) CreateSession(ctx context.Context, req dto.CreateSessionRequest, visitorID string) (dto.BookingSession, error) {
	if req.AdmissionToken == "" {
		return dto.BookingSession{}, inbound.Validation("admissionToken", "admissionToken wajib")
	}
	claims, err := uc.verifier.Verify(req.AdmissionToken)
	if err != nil {
		return dto.BookingSession{}, inbound.ErrAdmissionToken
	}
	if visitorID != "" && claims.VisitorID != "" && claims.VisitorID != visitorID {
		return dto.BookingSession{}, inbound.ErrVisitorMismatch
	}
	admission, err := uc.admissions.Activate(ctx, claims.JTI, uc.clock.Now())
	if err != nil {
		return dto.BookingSession{}, err
	}
	return uc.view(ctx, admission)
}

// GetSession returns the session by id for its owner.
func (uc *Session) GetSession(ctx context.Context, sessionID, visitorID string) (dto.BookingSession, error) {
	if !isUUID(sessionID) {
		return dto.BookingSession{}, inbound.Validation("sessionId", "sessionId harus UUID")
	}
	admission, err := uc.admissions.GetByID(ctx, sessionID)
	if err != nil {
		return dto.BookingSession{}, err
	}
	if visitorID != "" && admission.VisitorID != visitorID {
		return dto.BookingSession{}, inbound.ErrVisitorMismatch
	}
	if admission.IsExpired(uc.clock.Now()) {
		return dto.BookingSession{}, inbound.ErrSessionExpired
	}
	return uc.view(ctx, admission)
}

// GetCurrentSession recovers the active session for a visitor on an event (refresh/back navigation).
func (uc *Session) GetCurrentSession(ctx context.Context, eventID, visitorID string) (dto.BookingSession, error) {
	if !isUUID(eventID) {
		return dto.BookingSession{}, inbound.Validation("eventId", "eventId harus UUID")
	}
	admission, err := uc.admissions.FindActiveByVisitor(ctx, eventID, visitorID)
	if err != nil {
		return dto.BookingSession{}, err
	}
	return uc.view(ctx, admission)
}

func (uc *Session) view(ctx context.Context, admission entity.Admission) (dto.BookingSession, error) {
	detail, err := uc.events.GetDetail(ctx, admission.EventID)
	if err != nil {
		return dto.BookingSession{}, err
	}
	return mapper.ToBookingSession(admission, detail, nil, uc.clock.Now()), nil
}
