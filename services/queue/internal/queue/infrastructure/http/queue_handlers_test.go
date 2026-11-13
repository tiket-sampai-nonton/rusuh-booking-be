package http_test

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"regexp"
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
	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/queue/internal/queue/port/outbound"
)

// fakeAuth is a stub VisitorAuthenticator.
type fakeAuth struct {
	id string
	ok bool
}

func (f fakeAuth) Resolve(*http.Request) (string, bool) { return f.id, f.ok }
func (f fakeAuth) ResolveToken(*http.Request) (outbound.VisitorClaim, bool) {
	return outbound.VisitorClaim{VisitorID: f.id, Gen: 1}, f.ok
}
func (f fakeAuth) DeviceHash(r *http.Request) string {
	if r.Header.Get("X-Device-Id") == "" {
		return ""
	}
	return "dh-of-device"
}
func (f fakeAuth) GuestScope(r *http.Request, clientIP string) string {
	if dh := f.DeviceHash(r); dh != "" {
		return dh
	}
	return "ip-fake"
}

func TestJoinHandler_Created(t *testing.T) {
	ctrl := gomock.NewController(t)
	uc := mock_inbound.NewMockIJoinUsecase(ctrl)
	uc.EXPECT().Join(gomock.Any(), gomock.Any()).Return(dto.QueueEntry{EntryID: "e1", Seq: 5, Status: "WAITING", VisitorToken: "vt"}, nil)

	h := queuehttp.NewJoinHandler(uc, false, 1)
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

	h := queuehttp.NewJoinHandler(uc, false, 1)
	mux := http.NewServeMux()
	h.Register(mux)

	body := `{"buyerNik":"3201234567890123","email":"a@b.id","emailConfirmation":"a@b.id","consentNik":true}`
	req := httptest.NewRequest(http.MethodPost, "/api/v1/events/ev1/queue-entries", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Client-Type", "mobile")
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusCreated, rec.Code)
	assert.Contains(t, rec.Header().Get("Set-Cookie"), "rb_visitor=vt", "the cookie is always set (QVT-01)")
	assert.Contains(t, rec.Body.String(), `"visitorToken":"vt"`)
}

func TestJoinHandler_DomainError(t *testing.T) {
	ctrl := gomock.NewController(t)
	uc := mock_inbound.NewMockIJoinUsecase(ctrl)
	uc.EXPECT().Join(gomock.Any(), gomock.Any()).Return(dto.QueueEntry{}, entity.ErrAccountInQueue)

	h := queuehttp.NewJoinHandler(uc, false, 1)
	mux := http.NewServeMux()
	h.Register(mux)

	body := `{"buyerNik":"3201234567890123","email":"a@b.id","emailConfirmation":"a@b.id","consentNik":true}`
	req := httptest.NewRequest(http.MethodPost, "/api/v1/events/ev1/queue-entries", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusConflict, rec.Code)
	assert.Contains(t, rec.Body.String(), "ACCOUNT_ALREADY_IN_QUEUE")
}

// TC-QUE-39/40: the join gate errors reach the client as 409 EVENT_NOT_ON_SALE, 410 EVENT_SOLD_OUT and
// 503 DEPENDENCY_UNAVAILABLE, in the standard error envelope.
func TestJoinHandler_SaleGateErrors(t *testing.T) {
	tests := []struct {
		name     string
		err      error
		wantCode int
		wantBody string
	}{
		{"not on sale", entity.ErrNotOnSale, http.StatusConflict, "EVENT_NOT_ON_SALE"},
		{"sold out", entity.ErrSoldOut, http.StatusGone, "EVENT_SOLD_OUT"},
		{"sale window unavailable", entity.ErrDependencyUnavailable, http.StatusServiceUnavailable, "DEPENDENCY_UNAVAILABLE"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctrl := gomock.NewController(t)
			uc := mock_inbound.NewMockIJoinUsecase(ctrl)
			uc.EXPECT().Join(gomock.Any(), gomock.Any()).Return(dto.QueueEntry{}, tt.err)
			mux := http.NewServeMux()
			queuehttp.NewJoinHandler(uc, false, 1).Register(mux)
			req := httptest.NewRequest(http.MethodPost, "/api/v1/events/ev1/queue-entries", strings.NewReader(`{}`))
			req.Header.Set("Content-Type", "application/json")
			rec := httptest.NewRecorder()
			mux.ServeHTTP(rec, req)
			assert.Equal(t, tt.wantCode, rec.Code)
			assert.Contains(t, rec.Body.String(), fmt.Sprintf(`"meta":{"code":%d,"message":"%s:`, tt.wantCode, tt.wantBody))
			assert.Contains(t, rec.Body.String(), `"data":null`)
			assert.Empty(t, rec.Header().Get("Set-Cookie"), "a refused join issues no visitor cookie")
			assert.NotContains(t, rec.Body.String(), "valkey")
		})
	}
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
	uc.EXPECT().GetMyEntry(gomock.Any(), inbound.MyEntryInput{EventID: "5b1f2c9e-0000-4000-8000-000000000001", VisitorID: "v1", Gen: 1}).
		Return(dto.EntryStatus{EntryID: "e1", Seq: 5, Status: "WAITING"}, nil)

	h := queuehttp.NewMyEntryHandler(uc, fakeAuth{id: "v1", ok: true})
	mux := http.NewServeMux()
	h.Register(mux)
	req := httptest.NewRequest(http.MethodGet, "/api/v1/queue-entries/me?eventId=5b1f2c9e-0000-4000-8000-000000000001", nil)
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
	req := httptest.NewRequest(http.MethodGet, "/api/v1/queue-entries/me?eventId=5b1f2c9e-0000-4000-8000-000000000001", nil)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	assert.Equal(t, http.StatusUnauthorized, rec.Code, "R01.6: invalid token -> 401 VISITOR_TOKEN_INVALID")
	assert.Contains(t, rec.Body.String(), "VISITOR_TOKEN_INVALID")
}

func TestClaimHandler_OK(t *testing.T) {
	ctrl := gomock.NewController(t)
	uc := mock_inbound.NewMockIClaimUsecase(ctrl)
	uc.EXPECT().Claim(gomock.Any(), inbound.ClaimInput{EntryID: "e1", VisitorID: "v1", EventID: "5b1f2c9e-0000-4000-8000-000000000001", Gen: 1}).
		Return(dto.ClaimResponse{AdmissionToken: "adm", BookingURL: "u"}, nil)

	h := queuehttp.NewClaimHandler(uc, fakeAuth{id: "v1", ok: true})
	mux := http.NewServeMux()
	h.Register(mux)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/queue-entries/e1/claim?eventId=5b1f2c9e-0000-4000-8000-000000000001", nil)
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
	req := httptest.NewRequest(http.MethodPost, "/api/v1/queue-entries/e1/claim?eventId=5b1f2c9e-0000-4000-8000-000000000001", nil)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	assert.Equal(t, http.StatusGone, rec.Code)
	assert.Contains(t, rec.Body.String(), "TURN_MISSED")
}

func TestLeaveHandler_NoContent(t *testing.T) {
	ctrl := gomock.NewController(t)
	uc := mock_inbound.NewMockILeaveUsecase(ctrl)
	uc.EXPECT().Leave(gomock.Any(), inbound.LeaveInput{EntryID: "e1", VisitorID: "v1", EventID: "5b1f2c9e-0000-4000-8000-000000000001", Gen: 1}).Return(nil)

	h := queuehttp.NewLeaveHandler(uc, fakeAuth{id: "v1", ok: true})
	mux := http.NewServeMux()
	h.Register(mux)
	req := httptest.NewRequest(http.MethodDelete, "/api/v1/queue-entries/e1?eventId=5b1f2c9e-0000-4000-8000-000000000001", nil)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	require.Equal(t, http.StatusNoContent, rec.Code)
}

// TC-QUE-38 (P16): DELETE on a CLAIMED entry is 409 ENTRY_CLAIMED in the standard envelope, never a 204.
func TestLeaveHandler_ClaimedEntryIsConflict(t *testing.T) {
	ctrl := gomock.NewController(t)
	uc := mock_inbound.NewMockILeaveUsecase(ctrl)
	uc.EXPECT().Leave(gomock.Any(), gomock.Any()).Return(entity.ErrEntryClaimed)
	mux := http.NewServeMux()
	queuehttp.NewLeaveHandler(uc, fakeAuth{id: "v1", ok: true}).Register(mux)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest(http.MethodDelete, "/api/v1/queue-entries/e1?eventId=5b1f2c9e-0000-4000-8000-000000000001", nil))
	require.Equal(t, http.StatusConflict, rec.Code)
	assert.Contains(t, rec.Body.String(), `"meta":{"code":409,"message":"ENTRY_CLAIMED:`)
	assert.Contains(t, rec.Body.String(), `"data":null`)
}

func TestLeaveHandler_Unauthorized(t *testing.T) {
	ctrl := gomock.NewController(t)
	uc := mock_inbound.NewMockILeaveUsecase(ctrl)
	h := queuehttp.NewLeaveHandler(uc, fakeAuth{ok: false})
	mux := http.NewServeMux()
	h.Register(mux)
	req := httptest.NewRequest(http.MethodDelete, "/api/v1/queue-entries/e1?eventId=5b1f2c9e-0000-4000-8000-000000000001", nil)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	assert.Equal(t, http.StatusUnauthorized, rec.Code, "REQ-QVT-02: invalid token -> 401 VISITOR_TOKEN_INVALID")
	assert.Contains(t, rec.Body.String(), "VISITOR_TOKEN_INVALID")
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
			mux.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/api/v1/queue-entries/e1/claim?eventId=5b1f2c9e-0000-4000-8000-000000000001", nil))
			assert.Equal(t, tt.wantCode, rec.Code)
			assert.Contains(t, rec.Body.String(), tt.wantBody)
			assert.NotContains(t, rec.Body.String(), "valkey")
			if tt.wantCode == http.StatusNotFound {
				notFoundBody = rec.Body.String()
			}
		})
	}
	// An unauthenticated caller is 401 VISITOR_TOKEN_INVALID before any entry lookup, so it is no existence oracle
	// (REQ-QVT-02); a stranger with a valid token still gets the 404 above.
	ctrl := gomock.NewController(t)
	uc := mock_inbound.NewMockIClaimUsecase(ctrl)
	h := queuehttp.NewClaimHandler(uc, fakeAuth{ok: false})
	mux := http.NewServeMux()
	h.Register(mux)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/api/v1/queue-entries/e1/claim?eventId=5b1f2c9e-0000-4000-8000-000000000001", nil))
	assert.Equal(t, http.StatusUnauthorized, rec.Code)
	assert.Contains(t, rec.Body.String(), "VISITOR_TOKEN_INVALID")
	assert.Contains(t, notFoundBody, "NOT_FOUND")
}

func heartbeatServe(t *testing.T, uc *mock_inbound.MockIHeartbeatUsecase, auth fakeAuth) *httptest.ResponseRecorder {
	t.Helper()
	h := queuehttp.NewHeartbeatHandler(uc, auth)
	mux := http.NewServeMux()
	h.Register(mux)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/queue-entries/me/heartbeat?eventId=ev1", nil)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	return rec
}

func TestHeartbeatHandler_States(t *testing.T) {
	t.Run("ok", func(t *testing.T) {
		uc := mock_inbound.NewMockIHeartbeatUsecase(gomock.NewController(t))
		uc.EXPECT().Heartbeat(gomock.Any(), inbound.HeartbeatInput{EventID: "ev1", VisitorID: "v1", Gen: 1}).
			Return(dto.HeartbeatStatus{EntryID: "e1", State: "WAITING", NextHeartbeatMs: 30000}, nil)
		rec := heartbeatServe(t, uc, fakeAuth{id: "v1", ok: true})
		assert.Equal(t, http.StatusOK, rec.Code)
		assert.Equal(t, "no-store", rec.Header().Get("Cache-Control"))
		assert.Contains(t, rec.Body.String(), `"nextHeartbeatMs":30000`)
	})
	t.Run("bad token", func(t *testing.T) {
		uc := mock_inbound.NewMockIHeartbeatUsecase(gomock.NewController(t))
		rec := heartbeatServe(t, uc, fakeAuth{ok: false})
		assert.Equal(t, http.StatusUnauthorized, rec.Code)
	})
	t.Run("ended", func(t *testing.T) {
		uc := mock_inbound.NewMockIHeartbeatUsecase(gomock.NewController(t))
		uc.EXPECT().Heartbeat(gomock.Any(), gomock.Any()).Return(dto.HeartbeatStatus{}, entity.ErrEntryNotFound)
		rec := heartbeatServe(t, uc, fakeAuth{id: "v1", ok: true})
		assert.Equal(t, http.StatusNotFound, rec.Code)
		assert.Contains(t, rec.Body.String(), "ENTRY_NOT_FOUND")
	})
}

// TC-QUE-42 (REQ-QVT-02, P3): claim and leave with a missing/invalid token are 401 VISITOR_TOKEN_INVALID, with an
// identical body for an existing and a nonexistent entry id, and the usecase (store/Booking) is never reached.
func TestClaimAndLeaveHandlers_InvalidTokenIs401BeforeAnyLookup(t *testing.T) {
	routes := []struct {
		name   string
		method string
		path   func(id string) string
		mount  func(ctrl *gomock.Controller, mux *http.ServeMux) // strict mocks: any usecase call fails the test
	}{
		{"claim", http.MethodPost, func(id string) string {
			return "/api/v1/queue-entries/" + id + "/claim?eventId=5b1f2c9e-0000-4000-8000-000000000001"
		},
			func(ctrl *gomock.Controller, mux *http.ServeMux) {
				queuehttp.NewClaimHandler(mock_inbound.NewMockIClaimUsecase(ctrl), fakeAuth{ok: false}).Register(mux)
			}},
		{"leave", http.MethodDelete, func(id string) string {
			return "/api/v1/queue-entries/" + id + "?eventId=5b1f2c9e-0000-4000-8000-000000000001"
		},
			func(ctrl *gomock.Controller, mux *http.ServeMux) {
				queuehttp.NewLeaveHandler(mock_inbound.NewMockILeaveUsecase(ctrl), fakeAuth{ok: false}).Register(mux)
			}},
	}
	for _, rt := range routes {
		t.Run(rt.name, func(t *testing.T) {
			ctrl := gomock.NewController(t)
			mux := http.NewServeMux()
			rt.mount(ctrl, mux)
			var bodies []string
			for _, id := range []string{"existing-entry", "nonexistent-entry"} {
				rec := httptest.NewRecorder()
				mux.ServeHTTP(rec, httptest.NewRequest(rt.method, rt.path(id), nil))
				require.Equal(t, http.StatusUnauthorized, rec.Code)
				assert.Contains(t, rec.Body.String(), "VISITOR_TOKEN_INVALID")
				bodies = append(bodies, stripRequestID(rec.Body.String()))
			}
			assert.Equal(t, bodies[0], bodies[1], "identical for existing and nonexistent entry")
		})
	}
}

func stripRequestID(body string) string {
	return regexp.MustCompile(`"requestId":"[^"]*"`).ReplaceAllString(body, `"requestId":""`)
}

// D-C6.7: claim, leave and me answer 400 VALIDATION_ERROR for a missing, empty or non-UUID eventId. The check runs after
// the visitor token check and before the usecase: the strict mocks fail the test if any usecase is reached.
func TestVisitorRoutes_MissingEventIDIs400(t *testing.T) {
	routes := []struct {
		name   string
		method string
		path   string // without the query
		mount  func(ctrl *gomock.Controller, mux *http.ServeMux)
	}{
		{"claim", http.MethodPost, "/api/v1/queue-entries/e1/claim", func(ctrl *gomock.Controller, mux *http.ServeMux) {
			queuehttp.NewClaimHandler(mock_inbound.NewMockIClaimUsecase(ctrl), fakeAuth{id: "v1", ok: true}).Register(mux)
		}},
		{"leave", http.MethodDelete, "/api/v1/queue-entries/e1", func(ctrl *gomock.Controller, mux *http.ServeMux) {
			queuehttp.NewLeaveHandler(mock_inbound.NewMockILeaveUsecase(ctrl), fakeAuth{id: "v1", ok: true}).Register(mux)
		}},
		{"me", http.MethodGet, "/api/v1/queue-entries/me", func(ctrl *gomock.Controller, mux *http.ServeMux) {
			queuehttp.NewMyEntryHandler(mock_inbound.NewMockIMyEntryUsecase(ctrl), fakeAuth{id: "v1", ok: true}).Register(mux)
		}},
	}
	queries := []struct{ name, query string }{
		{"missing", ""},
		{"empty", "?eventId="},
		{"non-uuid", "?eventId=bukan-uuid"},
	}
	for _, rt := range routes {
		for _, q := range queries {
			t.Run(rt.name+"/"+q.name, func(t *testing.T) {
				ctrl := gomock.NewController(t)
				mux := http.NewServeMux()
				rt.mount(ctrl, mux)
				rec := httptest.NewRecorder()
				mux.ServeHTTP(rec, httptest.NewRequest(rt.method, rt.path+q.query, nil))
				require.Equal(t, http.StatusBadRequest, rec.Code)
				assert.Contains(t, rec.Body.String(), `"meta":{"code":400,"message":"VALIDATION_ERROR:`)
				assert.Contains(t, rec.Body.String(), "eventId")
				assert.Contains(t, rec.Body.String(), `"data":null`)
			})
		}
	}
}
