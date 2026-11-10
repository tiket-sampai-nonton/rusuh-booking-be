package usecase_test

import (
	"context"
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

func newSessionUC(admissions *fakeAdmissionRepo, events *fakeEventRepo, verifier *fakeTokenVerifier, clk fixedClock) *usecase.Session {
	return usecase.NewSession(admissions, events, verifier, clk)
}

func activeAdmissionForSession(id, visitorID, eventID string) entity.Admission {
	return entity.Admission{
		ID:                     id,
		EventID:                eventID,
		QueueEntryID:           "entry-1",
		VisitorID:              visitorID,
		Units:                  1,
		OutstandingUnits:       1,
		Status:                 vo.AdmissionActive,
		AdmissionJTI:           "alloc-1",
		CfgSelectionTTLSeconds: 300,
		CfgPaymentTTLSeconds:   600,
		ReservedAt:             time.Now(),
		ExpiresAt:              time.Now().Add(5 * time.Minute),
	}
}

func TestCreateSession_Success(t *testing.T) {
	a := activeAdmissionForSession("sess-1", "visitor-1", "event-1")
	a.Status = vo.AdmissionReserved // Activate transitions RESERVED→ACTIVE
	activated := a
	activated.Status = vo.AdmissionActive

	admRepo := &fakeAdmissionRepo{activated: activated}
	evRepo := &fakeEventRepo{}
	verifier := &fakeTokenVerifier{claims: outbound.AdmissionClaims{JTI: "alloc-1", EventID: "event-1"}}
	clk := fixedClock{t: time.Now()}

	uc := newSessionUC(admRepo, evRepo, verifier, clk)
	sess, err := uc.CreateSession(context.Background(), dto.CreateSessionRequest{AdmissionToken: "tok"}, "visitor-1")
	require.NoError(t, err)
	assert.Equal(t, "sess-1", sess.SessionID)
}

func TestCreateSession_MissingToken(t *testing.T) {
	uc := newSessionUC(&fakeAdmissionRepo{}, &fakeEventRepo{}, &fakeTokenVerifier{}, fixedClock{t: time.Now()})
	_, err := uc.CreateSession(context.Background(), dto.CreateSessionRequest{}, "visitor-1")
	require.Error(t, err)
	var ae *apperror.Error
	require.ErrorAs(t, err, &ae)
	assert.Equal(t, inbound.CodeValidation, ae.Code)
}

func TestCreateSession_InvalidToken(t *testing.T) {
	uc := newSessionUC(&fakeAdmissionRepo{}, &fakeEventRepo{}, &fakeTokenVerifier{err: inbound.ErrAdmissionToken}, fixedClock{t: time.Now()})
	_, err := uc.CreateSession(context.Background(), dto.CreateSessionRequest{AdmissionToken: "bad"}, "visitor-1")
	assert.ErrorIs(t, err, inbound.ErrAdmissionToken)
}

// TestCreateSession_ReclaimRecovery covers the re-entry scenario (TC-Q-REENTRY-01):
// buyer returns to waiting room → frontend re-claims → new JWT with same allocationID →
// Activate fails (admission already ACTIVE) → recovery via FindByJTI returns live session.
func TestCreateSession_ReclaimRecovery(t *testing.T) {
	a := activeAdmissionForSession("sess-1", "visitor-1", "event-1")
	// Activate fails: admission is already ACTIVE, not RESERVED
	admRepo := &fakeAdmissionRepo{
		activErr:  inbound.ErrAdmissionToken,
		admission: a, // FindByJTI returns the live ACTIVE admission
	}
	evRepo := &fakeEventRepo{}
	verifier := &fakeTokenVerifier{claims: outbound.AdmissionClaims{JTI: "alloc-1", EventID: "event-1"}}
	clk := fixedClock{t: time.Now()}

	uc := newSessionUC(admRepo, evRepo, verifier, clk)
	sess, err := uc.CreateSession(context.Background(), dto.CreateSessionRequest{AdmissionToken: "tok"}, "visitor-1")
	require.NoError(t, err)
	assert.Equal(t, "sess-1", sess.SessionID)
	assert.Equal(t, "SELECTING", sess.Phase)
}

// TestCreateSession_ReclaimRecovery_ExpiredSession: session sudah expired → tetap 403.
func TestCreateSession_ReclaimRecovery_ExpiredSession(t *testing.T) {
	a := activeAdmissionForSession("sess-1", "visitor-1", "event-1")
	a.ExpiresAt = time.Now().Add(-1 * time.Minute) // expired
	a.Status = vo.AdmissionExpired                 // terminal

	admRepo := &fakeAdmissionRepo{
		activErr:  inbound.ErrAdmissionToken,
		admission: a, // FindByJTI returns expired admission
	}
	evRepo := &fakeEventRepo{}
	verifier := &fakeTokenVerifier{claims: outbound.AdmissionClaims{JTI: "alloc-1", EventID: "event-1"}}
	clk := fixedClock{t: time.Now()}

	uc := newSessionUC(admRepo, evRepo, verifier, clk)
	_, err := uc.CreateSession(context.Background(), dto.CreateSessionRequest{AdmissionToken: "tok"}, "visitor-1")
	assert.ErrorIs(t, err, inbound.ErrAdmissionToken)
}

// TestCreateSession_ReclaimRecovery_WrongVisitor: visitor ID tidak cocok → tetap 403.
func TestCreateSession_ReclaimRecovery_WrongVisitor(t *testing.T) {
	a := activeAdmissionForSession("sess-1", "owner-visitor", "event-1")

	admRepo := &fakeAdmissionRepo{
		activErr:  inbound.ErrAdmissionToken,
		admission: a, // admission belongs to "owner-visitor"
	}
	evRepo := &fakeEventRepo{}
	verifier := &fakeTokenVerifier{claims: outbound.AdmissionClaims{JTI: "alloc-1", EventID: "event-1"}}
	clk := fixedClock{t: time.Now()}

	uc := newSessionUC(admRepo, evRepo, verifier, clk)
	// different visitor tries to use the same token
	_, err := uc.CreateSession(context.Background(), dto.CreateSessionRequest{AdmissionToken: "tok"}, "attacker-visitor")
	assert.ErrorIs(t, err, inbound.ErrAdmissionToken)
}
