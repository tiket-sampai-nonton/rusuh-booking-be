package usecase_test

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

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
