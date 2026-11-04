package http_test

import (
	stdhttp "net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	mock_inbound "github.com/tiket-sampai-nonton/rusuh-booking-be/services/queue/_mock/queue/port/inbound"
	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/queue/internal/queue/domain/dto"
	queuehttp "github.com/tiket-sampai-nonton/rusuh-booking-be/services/queue/internal/queue/infrastructure/http"
	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/queue/internal/queue/port/inbound"
)

const joinBody = `{"buyerNik":"3201234567890123","email":"a@b.id","emailConfirmation":"a@b.id","consentNik":true}`

func joinVisitorIDSeen(t *testing.T, auth queuehttp.VisitorAuthenticator, mutate func(*stdhttp.Request)) string {
	t.Helper()
	ctrl := gomock.NewController(t)
	uc := mock_inbound.NewMockIJoinUsecase(ctrl)
	var seen string
	uc.EXPECT().Join(gomock.Any(), gomock.Any()).DoAndReturn(func(_ interface{ Done() <-chan struct{} }, in inbound.JoinInput) (dto.QueueEntry, error) {
		seen = in.VisitorID
		return dto.QueueEntry{EntryID: "e1"}, nil
	})
	h := queuehttp.NewJoinHandler(uc, false)
	if auth != nil {
		h = h.WithAuth(auth)
	}
	mux := stdhttp.NewServeMux()
	h.Register(mux)
	req := httptest.NewRequest(stdhttp.MethodPost, "/api/v1/events/ev1/queue-entries", strings.NewReader(joinBody))
	req.Header.Set("Content-Type", "application/json")
	mutate(req)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	require.Equal(t, stdhttp.StatusCreated, rec.Code)
	return seen
}

// TC-QUE-29 (INV-3): an unverified visitor id from a cookie or header is never used. Before the fix a forged
// X-Visitor-ID of a victim's visitor id made join answer VISITOR_ALREADY_IN_QUEUE for the attacker and, worse, could
// create an entry owned by the victim's visitor id.
func TestJoinHandler_ForgedVisitorHeaderIsNeverUsed(t *testing.T) {
	seen := joinVisitorIDSeen(t, fakeAuth{id: "", ok: false}, func(r *stdhttp.Request) { r.Header.Set("X-Visitor-ID", "victim-visitor-uuid") })
	assert.Empty(t, seen)
}

func TestJoinHandler_ForgedVisitorCookieIsNeverUsed(t *testing.T) {
	seen := joinVisitorIDSeen(t, fakeAuth{id: "", ok: false}, func(r *stdhttp.Request) {
		r.AddCookie(&stdhttp.Cookie{Name: "rb_visitor", Value: "forged.jwt.value"})
	})
	assert.Empty(t, seen)
}

func TestJoinHandler_HeaderIsIgnoredWithoutAnAuthenticator(t *testing.T) {
	seen := joinVisitorIDSeen(t, nil, func(r *stdhttp.Request) { r.Header.Set("X-Visitor-ID", "victim-visitor-uuid") })
	assert.Empty(t, seen, "tanpa authenticator, id yang tak terverifikasi tidak dipakai")
}

func TestJoinHandler_VerifiedVisitorTokenIsUsed(t *testing.T) {
	seen := joinVisitorIDSeen(t, fakeAuth{id: "visitor-sub", ok: true}, func(r *stdhttp.Request) {
		r.AddCookie(&stdhttp.Cookie{Name: "rb_visitor", Value: "valid.jwt"})
	})
	assert.Equal(t, "visitor-sub", seen)
}

func TestJoinHandler_NoVisitorCredentialStillJoins(t *testing.T) {
	seen := joinVisitorIDSeen(t, fakeAuth{id: "", ok: false}, func(*stdhttp.Request) {})
	assert.Empty(t, seen)
}
