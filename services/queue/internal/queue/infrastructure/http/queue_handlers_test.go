package http_test

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	mock_inbound "github.com/tiket-sampai-nonton/rusuh-booking-be/services/queue/_mock/queue/port/inbound"
	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/queue/internal/queue/domain/dto"
	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/queue/internal/queue/domain/entity"
	queuehttp "github.com/tiket-sampai-nonton/rusuh-booking-be/services/queue/internal/queue/infrastructure/http"
	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/queue/internal/queue/port/inbound"
)

// fakeAuth is a stub VisitorAuthenticator.
type fakeAuth struct {
	id string
	ok bool
}

func (f fakeAuth) Resolve(*http.Request) (string, bool) { return f.id, f.ok }

func TestJoinHandler_Created(t *testing.T) {
	ctrl := gomock.NewController(t)
	uc := mock_inbound.NewMockIJoinUsecase(ctrl)
	uc.EXPECT().Join(gomock.Any(), gomock.Any()).Return(dto.QueueEntry{EntryID: "e1", Seq: 5, Status: "WAITING", VisitorToken: "vt"}, nil)

	h := queuehttp.NewJoinHandler(uc, false)
	mux := http.NewServeMux()
	h.Register(mux)

	body := `{"buyerNik":"3201234567890123","email":"a@b.id","emailConfirmation":"a@b.id","consentNik":true}`
	req := httptest.NewRequest(http.MethodPost, "/api/v1/events/ev1/queue-entries", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusCreated, rec.Code)
	assert.Equal(t, "no-store", rec.Header().Get("Cache-Control"))
	// web client: visitor token moved to HttpOnly cookie, not in body
	assert.Contains(t, rec.Header().Get("Set-Cookie"), "rb_visitor=vt")
	assert.NotContains(t, rec.Body.String(), "visitorToken")
}

func TestJoinHandler_MobileReturnsTokenInBody(t *testing.T) {
	ctrl := gomock.NewController(t)
	uc := mock_inbound.NewMockIJoinUsecase(ctrl)
	uc.EXPECT().Join(gomock.Any(), gomock.Any()).Return(dto.QueueEntry{EntryID: "e1", VisitorToken: "vt"}, nil)

	h := queuehttp.NewJoinHandler(uc, false)
	mux := http.NewServeMux()
	h.Register(mux)

	body := `{"buyerNik":"3201234567890123","email":"a@b.id","emailConfirmation":"a@b.id","consentNik":true}`
	req := httptest.NewRequest(http.MethodPost, "/api/v1/events/ev1/queue-entries", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Client-Type", "mobile")
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusCreated, rec.Code)
	assert.Empty(t, rec.Header().Get("Set-Cookie"))
	assert.Contains(t, rec.Body.String(), `"visitorToken":"vt"`)
}

func TestJoinHandler_DomainError(t *testing.T) {
	ctrl := gomock.NewController(t)
	uc := mock_inbound.NewMockIJoinUsecase(ctrl)
	uc.EXPECT().Join(gomock.Any(), gomock.Any()).Return(dto.QueueEntry{}, entity.ErrNikInQueue)

	h := queuehttp.NewJoinHandler(uc, false)
	mux := http.NewServeMux()
	h.Register(mux)

	body := `{"buyerNik":"3201234567890123","email":"a@b.id","emailConfirmation":"a@b.id","consentNik":true}`
	req := httptest.NewRequest(http.MethodPost, "/api/v1/events/ev1/queue-entries", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusConflict, rec.Code)
	assert.Contains(t, rec.Body.String(), "NIK_ALREADY_IN_QUEUE")
}

func TestStatusHandler_OK(t *testing.T) {
	ctrl := gomock.NewController(t)
	uc := mock_inbound.NewMockIStatusUsecase(ctrl)
	uc.EXPECT().GetStatus(gomock.Any(), "ev1").Return(dto.StatusDocument{EventID: "ev1", SaleState: "OPEN"}, nil)

	h := queuehttp.NewStatusHandler(uc)
	mux := http.NewServeMux()
	h.Register(mux)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/queue-status/ev1", nil)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusOK, rec.Code)
	assert.Equal(t, "public, max-age=1, stale-while-revalidate=2", rec.Header().Get("Cache-Control"))
}

func TestStatusHandler_NotFound(t *testing.T) {
	ctrl := gomock.NewController(t)
	uc := mock_inbound.NewMockIStatusUsecase(ctrl)
	uc.EXPECT().GetStatus(gomock.Any(), "ev1").Return(dto.StatusDocument{}, entity.ErrNotFound)

	h := queuehttp.NewStatusHandler(uc)
	mux := http.NewServeMux()
	h.Register(mux)
	req := httptest.NewRequest(http.MethodGet, "/api/v1/queue-status/ev1", nil)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	assert.Equal(t, http.StatusNotFound, rec.Code)
}

func TestMyEntryHandler_OK(t *testing.T) {
	ctrl := gomock.NewController(t)
	uc := mock_inbound.NewMockIMyEntryUsecase(ctrl)
	uc.EXPECT().GetMyEntry(gomock.Any(), inbound.MyEntryInput{EventID: "ev1", VisitorID: "v1"}).
		Return(dto.EntryStatus{EntryID: "e1", Seq: 5, Status: "WAITING"}, nil)

	h := queuehttp.NewMyEntryHandler(uc, fakeAuth{id: "v1", ok: true})
	mux := http.NewServeMux()
	h.Register(mux)
	req := httptest.NewRequest(http.MethodGet, "/api/v1/queue-entries/me?eventId=ev1", nil)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	assert.Equal(t, http.StatusOK, rec.Code)
}

func TestMyEntryHandler_Unauthorized(t *testing.T) {
	ctrl := gomock.NewController(t)
	uc := mock_inbound.NewMockIMyEntryUsecase(ctrl)
	h := queuehttp.NewMyEntryHandler(uc, fakeAuth{ok: false})
	mux := http.NewServeMux()
	h.Register(mux)
	req := httptest.NewRequest(http.MethodGet, "/api/v1/queue-entries/me?eventId=ev1", nil)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	assert.Equal(t, http.StatusNotFound, rec.Code)
}

func TestClaimHandler_OK(t *testing.T) {
	ctrl := gomock.NewController(t)
	uc := mock_inbound.NewMockIClaimUsecase(ctrl)
	uc.EXPECT().Claim(gomock.Any(), inbound.ClaimInput{EntryID: "e1", VisitorID: "v1", EventID: "ev1"}).
		Return(dto.ClaimResponse{AdmissionToken: "adm", BookingURL: "u"}, nil)

	h := queuehttp.NewClaimHandler(uc, fakeAuth{id: "v1", ok: true})
	mux := http.NewServeMux()
	h.Register(mux)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/queue-entries/e1/claim?eventId=ev1", nil)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	assert.Equal(t, http.StatusOK, rec.Code)
	assert.Contains(t, rec.Body.String(), "adm")
}

func TestClaimHandler_TurnMissedGone(t *testing.T) {
	ctrl := gomock.NewController(t)
	uc := mock_inbound.NewMockIClaimUsecase(ctrl)
	uc.EXPECT().Claim(gomock.Any(), gomock.Any()).Return(dto.ClaimResponse{}, entity.ErrTurnMissed)

	h := queuehttp.NewClaimHandler(uc, fakeAuth{id: "v1", ok: true})
	mux := http.NewServeMux()
	h.Register(mux)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/queue-entries/e1/claim?eventId=ev1", nil)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	assert.Equal(t, http.StatusGone, rec.Code)
	assert.Contains(t, rec.Body.String(), "TURN_MISSED")
}

func TestLeaveHandler_NoContent(t *testing.T) {
	ctrl := gomock.NewController(t)
	uc := mock_inbound.NewMockILeaveUsecase(ctrl)
	uc.EXPECT().Leave(gomock.Any(), inbound.LeaveInput{EntryID: "e1", VisitorID: "v1", EventID: "ev1"}).Return(nil)

	h := queuehttp.NewLeaveHandler(uc, fakeAuth{id: "v1", ok: true})
	mux := http.NewServeMux()
	h.Register(mux)
	req := httptest.NewRequest(http.MethodDelete, "/api/v1/queue-entries/e1?eventId=ev1", nil)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	require.Equal(t, http.StatusNoContent, rec.Code)
}

func TestLeaveHandler_Unauthorized(t *testing.T) {
	ctrl := gomock.NewController(t)
	uc := mock_inbound.NewMockILeaveUsecase(ctrl)
	h := queuehttp.NewLeaveHandler(uc, fakeAuth{ok: false})
	mux := http.NewServeMux()
	h.Register(mux)
	req := httptest.NewRequest(http.MethodDelete, "/api/v1/queue-entries/e1?eventId=ev1", nil)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	assert.Equal(t, http.StatusNotFound, rec.Code)
}

// QSH-T10: claim error mapping. A WAITING entry is "not admitted yet" (409), a wrong visitor is answered exactly like an
// unknown entry (404, same body), and the body never carries internal error text.
func TestClaimHandler_ErrorMapping(t *testing.T) {
	tests := []struct {
		name     string
		err      error
		wantCode int
		wantBody string
	}{
		{"waiting entry", entity.ErrNotAdmitted, http.StatusConflict, "NOT_ADMITTED"},
		{"missed", entity.ErrTurnMissed, http.StatusGone, "TURN_MISSED"},
		{"left/ended/completed or unknown or wrong visitor", entity.ErrNotFound, http.StatusNotFound, "NOT_FOUND"},
	}
	var notFoundBody string
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctrl := gomock.NewController(t)
			uc := mock_inbound.NewMockIClaimUsecase(ctrl)
			uc.EXPECT().Claim(gomock.Any(), gomock.Any()).Return(dto.ClaimResponse{}, tt.err)
			h := queuehttp.NewClaimHandler(uc, fakeAuth{id: "v1", ok: true})
			mux := http.NewServeMux()
			h.Register(mux)
			rec := httptest.NewRecorder()
			mux.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/api/v1/queue-entries/e1/claim?eventId=ev1", nil))
			assert.Equal(t, tt.wantCode, rec.Code)
			assert.Contains(t, rec.Body.String(), tt.wantBody)
			assert.NotContains(t, rec.Body.String(), "valkey")
			if tt.wantCode == http.StatusNotFound {
				notFoundBody = rec.Body.String()
			}
		})
	}
	// An unauthenticated caller gets the same NOT_FOUND family as a stranger (no existence oracle).
	ctrl := gomock.NewController(t)
	uc := mock_inbound.NewMockIClaimUsecase(ctrl)
	h := queuehttp.NewClaimHandler(uc, fakeAuth{ok: false})
	mux := http.NewServeMux()
	h.Register(mux)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/api/v1/queue-entries/e1/claim?eventId=ev1", nil))
	assert.Equal(t, http.StatusNotFound, rec.Code)
	assert.Contains(t, notFoundBody, "NOT_FOUND")
}
