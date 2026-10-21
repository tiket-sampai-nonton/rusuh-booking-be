package http_test

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"go.uber.org/mock/gomock"

	mock_inbound "github.com/tiket-sampai-nonton/rusuh-booking-be/services/notification/_mock/notification/port/inbound"
	notificationhttp "github.com/tiket-sampai-nonton/rusuh-booking-be/services/notification/internal/notification/infrastructure/http"
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
