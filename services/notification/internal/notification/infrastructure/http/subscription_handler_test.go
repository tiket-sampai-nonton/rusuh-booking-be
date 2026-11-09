package http_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	mock_inbound "github.com/tiket-sampai-nonton/rusuh-booking-be/services/notification/_mock/notification/port/inbound"
	notificationhttp "github.com/tiket-sampai-nonton/rusuh-booking-be/services/notification/internal/notification/infrastructure/http"
	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/notification/internal/notification/port/inbound"
)

type fakeAuth struct {
	visitorID string
	ok        bool
}

func (f fakeAuth) Resolve(*http.Request) (string, bool) { return f.visitorID, f.ok }

func newServer(t *testing.T, uc *mock_inbound.MockISubscriptionUsecase, auth notificationhttp.VisitorAuthenticator) http.Handler {
	t.Helper()
	mux := http.NewServeMux()
	notificationhttp.NewSubscriptionHandler(uc, auth).Register(mux)
	return mux
}

func TestSubscribe_204(t *testing.T) {
	ctrl := gomock.NewController(t)
	uc := mock_inbound.NewMockISubscriptionUsecase(ctrl)
	uc.EXPECT().Subscribe(gomock.Any(), "v1", gomock.Any()).Return(nil)

	srv := newServer(t, uc, fakeAuth{visitorID: "v1", ok: true})
	body := `{"eventId":"e1","platform":"ANDROID","pushToken":"tok"}`
	req := httptest.NewRequest(http.MethodPost, "/api/v1/push-subscriptions", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusNoContent, rec.Code)
}

func TestSubscribe_Unauthorized(t *testing.T) {
	ctrl := gomock.NewController(t)
	uc := mock_inbound.NewMockISubscriptionUsecase(ctrl) // no call expected

	srv := newServer(t, uc, fakeAuth{ok: false})
	req := httptest.NewRequest(http.MethodPost, "/api/v1/push-subscriptions", strings.NewReader(`{}`))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusUnauthorized, rec.Code)
}

func TestUnsubscribe_204(t *testing.T) {
	ctrl := gomock.NewController(t)
	uc := mock_inbound.NewMockISubscriptionUsecase(ctrl)
	uc.EXPECT().Unsubscribe(gomock.Any(), "v1", "e1").Return(nil)

	srv := newServer(t, uc, fakeAuth{visitorID: "v1", ok: true})
	req := httptest.NewRequest(http.MethodDelete, "/api/v1/push-subscriptions/e1", nil)
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusNoContent, rec.Code)
}

func TestUnsubscribe_Unauthorized(t *testing.T) {
	ctrl := gomock.NewController(t)
	uc := mock_inbound.NewMockISubscriptionUsecase(ctrl)

	srv := newServer(t, uc, fakeAuth{ok: false})
	req := httptest.NewRequest(http.MethodDelete, "/api/v1/push-subscriptions/e1", nil)
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusUnauthorized, rec.Code)
}

func errorMessage(t *testing.T, rec *httptest.ResponseRecorder) string {
	t.Helper()
	var env struct {
		Meta struct {
			Message string `json:"message"`
		} `json:"meta"`
	}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &env), rec.Body.String())
	return env.Meta.Message
}

// TC-API-03 (SRS-IF-30/33): POST /push-subscriptions really answers the 400 and 401 that notification.yaml documents.
func TestSubscribe_DocumentedErrorResponses(t *testing.T) {
	tests := []struct {
		name   string
		auth   fakeAuth
		body   string
		useErr error // returned by the usecase when it is reached
		reach  bool
		status int
		prefix string
	}{
		{"malformed JSON", fakeAuth{visitorID: "v1", ok: true}, `{bukan`, nil, false, 400, "VALIDATION_ERROR: "},
		{"unknown field", fakeAuth{visitorID: "v1", ok: true}, `{"eventId":"e1","x":1}`, nil, false, 400, "VALIDATION_ERROR: "},
		{"invalid payload", fakeAuth{visitorID: "v1", ok: true}, `{"eventId":"","platform":"","pushToken":""}`,
			inbound.ValidationError(map[string]string{"eventId": "wajib"}), true, 400, "VALIDATION_ERROR: "},
		// The 401 body carries errorCode VALIDATION_ERROR today (inbound.ErrUnauthorized), not UNAUTHORIZED.
		{"no visitor token", fakeAuth{ok: false}, `{}`, nil, false, 401, "VALIDATION_ERROR: "},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			uc := mock_inbound.NewMockISubscriptionUsecase(gomock.NewController(t))
			if tc.reach {
				uc.EXPECT().Subscribe(gomock.Any(), "v1", gomock.Any()).Return(tc.useErr)
			}
			req := httptest.NewRequest(http.MethodPost, "/api/v1/push-subscriptions", strings.NewReader(tc.body))
			req.Header.Set("Content-Type", "application/json")
			rec := httptest.NewRecorder()
			newServer(t, uc, tc.auth).ServeHTTP(rec, req)
			assert.Equal(t, tc.status, rec.Code, rec.Body.String())
			assert.True(t, strings.HasPrefix(errorMessage(t, rec), tc.prefix), errorMessage(t, rec))
		})
	}
}
