package usecase_test

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tiket-sampai-nonton/rusuh-booking-be/pkg/apperror"
	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/booking/internal/booking/application/usecase"
	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/booking/internal/booking/domain/entity"
	vo "github.com/tiket-sampai-nonton/rusuh-booking-be/services/booking/internal/booking/domain/valueobject"
	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/booking/internal/booking/port/inbound"
)

func TestCancelSession_Releases(t *testing.T) {
	adm := entity.Admission{ID: sessionID, VisitorID: visitor, Status: vo.AdmissionActive, ExpiresAt: time.Now().Add(time.Hour)}
	repo := &fakeAdmissionRepo{admission: adm}
	uc := usecase.NewCancelSession(repo)
	err := uc.CancelSession(context.Background(), sessionID, visitor)
	require.NoError(t, err)
	assert.Contains(t, repo.released, sessionID)
}

func TestCancelSession_NotFound(t *testing.T) {
	uc := usecase.NewCancelSession(&fakeAdmissionRepo{getErr: inbound.ErrNotFound})
	err := uc.CancelSession(context.Background(), sessionID, visitor)
	assert.ErrorIs(t, err, inbound.ErrNotFound)
}

func TestCancelSession_VisitorMismatch(t *testing.T) {
	adm := entity.Admission{ID: sessionID, VisitorID: "other", Status: vo.AdmissionActive}
	uc := usecase.NewCancelSession(&fakeAdmissionRepo{admission: adm})
	err := uc.CancelSession(context.Background(), sessionID, visitor)
	assert.ErrorIs(t, err, inbound.ErrVisitorMismatch)
}

func TestCancelSession_AlreadyTerminal_IsNoOp(t *testing.T) {
	adm := entity.Admission{ID: sessionID, VisitorID: visitor, Status: vo.AdmissionExpired}
	uc := usecase.NewCancelSession(&fakeAdmissionRepo{admission: adm})
	err := uc.CancelSession(context.Background(), sessionID, visitor)
	assert.NoError(t, err)
}

// REQ-4: a malformed sessionId is rejected with VALIDATION_ERROR (400) BEFORE the admission repo is touched;
// a well-formed-but-absent UUID still reaches the repo and surfaces 404.
func TestCancelSession_PathUUIDValidation(t *testing.T) {
	tests := []struct {
		name      string
		sessionID string
		wantField string
		repoErr   error
		wantErrIs error
	}{
		{name: "non-uuid rejected before repo", sessionID: "not-a-uuid", wantField: "sessionId"},
		{name: "valid-but-absent uuid reaches repo and 404s", sessionID: sessionID, repoErr: inbound.ErrNotFound, wantErrIs: inbound.ErrNotFound},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			repo := &fakeAdmissionRepo{getErr: tt.repoErr}
			uc := usecase.NewCancelSession(repo)
			err := uc.CancelSession(context.Background(), tt.sessionID, visitor)
			require.Error(t, err)
			if tt.wantField != "" {
				assert.Equal(t, apperror.CodeValidationFailed, codeOf(t, err))
				assert.Equal(t, tt.wantField, validationField(t, err))
				assert.Zero(t, repo.getCalls, "repo must not be called for a malformed id")
				return
			}
			assert.ErrorIs(t, err, tt.wantErrIs)
			assert.Positive(t, repo.getCalls, "valid uuid must reach the repo")
		})
	}
}

// TC-QUE-37 (P4, 1.4): cancel releases with LEFT_WITHOUT_BUYING; a foreign visitor never reaches Release.
func TestCancelSession_ReleaseReasonAndOwnership(t *testing.T) {
	tests := []struct {
		name        string
		admVisitor  string
		releaseErr  error
		wantReasons []vo.EndReason
		wantErr     error
	}{
		{name: "owner releases as left-without-buying", admVisitor: visitor, wantReasons: []vo.EndReason{vo.EndLeftWithoutBuying}},
		{name: "foreign visitor never reaches the repo release", admVisitor: "other", wantErr: inbound.ErrVisitorMismatch},
		{name: "editable session surfaces ErrBookingLocked", admVisitor: visitor, releaseErr: inbound.ErrBookingLocked, wantErr: inbound.ErrBookingLocked},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			repo := &fakeAdmissionRepo{
				admission:  entity.Admission{ID: sessionID, VisitorID: tt.admVisitor, Status: vo.AdmissionActive},
				releaseErr: tt.releaseErr,
			}
			err := usecase.NewCancelSession(repo).CancelSession(context.Background(), sessionID, visitor)
			if tt.wantErr != nil {
				assert.ErrorIs(t, err, tt.wantErr)
			} else {
				require.NoError(t, err)
			}
			assert.Equal(t, tt.wantReasons, repo.releaseReasons)
		})
	}
}
