package http_test

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	mock_inbound "github.com/tiket-sampai-nonton/rusuh-booking-be/services/queue/_mock/queue/port/inbound"
	mock_outbound "github.com/tiket-sampai-nonton/rusuh-booking-be/services/queue/_mock/queue/port/outbound"
	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/queue/internal/queue/domain/dto"
	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/queue/internal/queue/domain/entity"
	queuehttp "github.com/tiket-sampai-nonton/rusuh-booking-be/services/queue/internal/queue/infrastructure/http"
	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/queue/internal/queue/port/inbound"
	shared "github.com/tiket-sampai-nonton/rusuh-booking-be/services/queue/internal/shared/entity"
)

const (
	adminEvent = "11111111-1111-1111-1111-111111111111"
	adminPath  = "/internal/v1/admin/events/" + adminEvent + "/queue-settings"
)

type adminFixture struct {
	get      *mock_inbound.MockIGetQueueSettingsUsecase
	set      *mock_inbound.MockISetQueueSettingsUsecase
	verifier *mock_outbound.MockIAdminTokenVerifier
	mux      *http.ServeMux
}

func newAdminFixture(t *testing.T) *adminFixture {
	t.Helper()
	ctrl := gomock.NewController(t)
	f := &adminFixture{
		get:      mock_inbound.NewMockIGetQueueSettingsUsecase(ctrl),
		set:      mock_inbound.NewMockISetQueueSettingsUsecase(ctrl),
		verifier: mock_outbound.NewMockIAdminTokenVerifier(ctrl),
		mux:      http.NewServeMux(),
	}
	queuehttp.NewSettingsAdminHandler(f.get, f.set, f.verifier).Register(f.mux)
	return f
}

func (f *adminFixture) allowAdmin() {
	f.verifier.EXPECT().VerifyAdmin("good").Return("acct-1", nil).AnyTimes()
}

func doAdmin(mux *http.ServeMux, method, path, body string, hdr map[string]string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	for k, v := range hdr {
		req.Header.Set(k, v)
	}
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	return rec
}

var bearerGood = map[string]string{"Authorization": "Bearer good"}

// envelope is the standard response envelope: errors carry "KODE: pesan" in meta.message (pkg/apperror).
type envelope struct {
	Meta struct {
		Code    int    `json:"code"`
		Message string `json:"message"`
	} `json:"meta"`
	Data json.RawMessage `json:"data"`
}

func (e envelope) errCode() string {
	code, _, _ := strings.Cut(e.Meta.Message, ":")
	return code
}

func decodeEnv(t *testing.T, rec *httptest.ResponseRecorder) envelope {
	t.Helper()
	var e envelope
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &e), rec.Body.String())
	return e
}

func TestSettingsAdmin_GetOK(t *testing.T) {
	f := newAdminFixture(t)
	f.allowAdmin()
	f.get.EXPECT().Get(gomock.Any(), adminEvent).Return(dto.QueueSettings{EventID: adminEvent, ActiveSlotCapacity: 12, UpdatedAt: "2026-01-02T03:04:05Z"}, nil)
	rec := doAdmin(f.mux, http.MethodGet, adminPath, "", bearerGood)
	assert.Equal(t, http.StatusOK, rec.Code)
	assert.Equal(t, "no-store", rec.Header().Get("Cache-Control"))
	var got dto.QueueSettings
	require.NoError(t, json.Unmarshal(decodeEnv(t, rec).Data, &got))
	assert.Equal(t, 12, got.ActiveSlotCapacity)
	assert.Equal(t, "2026-01-02T03:04:05Z", got.UpdatedAt)
}

func TestSettingsAdmin_PutCreatedVsUpdated(t *testing.T) {
	for _, tc := range []struct {
		name    string
		created bool
		want    int
	}{{"create is 201", true, http.StatusCreated}, {"update or repeat is 200", false, http.StatusOK}} {
		t.Run(tc.name, func(t *testing.T) {
			f := newAdminFixture(t)
			f.allowAdmin()
			f.set.EXPECT().Set(gomock.Any(), gomock.Any()).DoAndReturn(func(_ any, in inbound.SetSettingsInput) (dto.QueueSettings, bool, error) {
				assert.Equal(t, adminEvent, in.EventID)
				assert.Equal(t, "acct-1", in.Actor, "actor = verified token subject")
				require.NotNil(t, in.Patch.ActiveSlotCapacity)
				assert.Equal(t, 40, *in.Patch.ActiveSlotCapacity)
				require.NotNil(t, in.Patch.OneActivePerEmail)
				assert.False(t, *in.Patch.OneActivePerEmail)
				assert.Nil(t, in.Patch.PollFarMs)
				return dto.QueueSettings{EventID: adminEvent, ActiveSlotCapacity: 40}, tc.created, nil
			})
			rec := doAdmin(f.mux, http.MethodPut, adminPath, `{"activeSlotCapacity":40,"oneActivePerEmail":false}`, bearerGood)
			assert.Equal(t, tc.want, rec.Code)
			assert.Equal(t, "no-store", rec.Header().Get("Cache-Control"))
		})
	}
}

func TestSettingsAdmin_AuthRejectsBeforeAnythingElse(t *testing.T) {
	cases := []struct {
		name   string
		hdr    map[string]string
		verify func(f *adminFixture)
	}{
		{"no bearer", nil, func(f *adminFixture) {}},
		{"basic scheme", map[string]string{"Authorization": "Basic abc"}, func(f *adminFixture) {}},
		{"garbage", map[string]string{"Authorization": "Bearer garbage"}, func(f *adminFixture) {
			f.verifier.EXPECT().VerifyAdmin("garbage").Return("", errors.New("bad"))
		}},
		{"wrong scope or not allow-listed", map[string]string{"Authorization": "Bearer other"}, func(f *adminFixture) {
			f.verifier.EXPECT().VerifyAdmin("other").Return("", errors.New("denied"))
		}},
		{"verifier returns empty subject", map[string]string{"Authorization": "Bearer empty"}, func(f *adminFixture) {
			f.verifier.EXPECT().VerifyAdmin("empty").Return("", nil)
		}},
	}
	for _, c := range cases {
		for _, method := range []string{http.MethodGet, http.MethodPut} {
			t.Run(c.name+"/"+method, func(t *testing.T) {
				f := newAdminFixture(t)
				c.verify(f)
				f.get.EXPECT().Get(gomock.Any(), gomock.Any()).Times(0)
				f.set.EXPECT().Set(gomock.Any(), gomock.Any()).Times(0)
				path := adminPath
				body := `{"activeSlotCapacity":5}`
				if c.name == "no bearer" {
					path = "/internal/v1/admin/events/bukan-uuid/queue-settings" // auth is evaluated before the path check
					body = "{{{"
				}
				rec := doAdmin(f.mux, method, path, body, c.hdr)
				assert.Equal(t, http.StatusUnauthorized, rec.Code)
				assert.NotEmpty(t, rec.Header().Get("WWW-Authenticate"))
				assert.Equal(t, "UNAUTHORIZED", decodeEnv(t, rec).errCode())
			})
		}
	}
}

func TestSettingsAdmin_NilVerifierDeniesAll(t *testing.T) {
	ctrl := gomock.NewController(t)
	get := mock_inbound.NewMockIGetQueueSettingsUsecase(ctrl)
	set := mock_inbound.NewMockISetQueueSettingsUsecase(ctrl)
	mux := http.NewServeMux()
	queuehttp.NewSettingsAdminHandler(get, set, nil).Register(mux)
	for _, m := range []string{http.MethodGet, http.MethodPut} {
		rec := doAdmin(mux, m, adminPath, `{"activeSlotCapacity":5}`, bearerGood)
		assert.Equal(t, http.StatusUnauthorized, rec.Code, m)
		assert.NotEmpty(t, rec.Header().Get("WWW-Authenticate"))
	}
}

func TestSettingsAdmin_BadRequests(t *testing.T) {
	cases := []struct {
		name   string
		method string
		path   string
		body   string
	}{
		{"get bad uuid", http.MethodGet, "/internal/v1/admin/events/bukan-uuid/queue-settings", ""},
		{"put bad uuid", http.MethodPut, "/internal/v1/admin/events/bukan-uuid/queue-settings", `{"activeSlotCapacity":5}`},
		{"malformed json", http.MethodPut, adminPath, `{"activeSlotCapacity":`},
		{"empty body", http.MethodPut, adminPath, ``},
		{"unknown field", http.MethodPut, adminPath, `{"activeSlotCapacity":5,"foo":1}`},
		{"actor in body is an unknown field", http.MethodPut, adminPath, `{"activeSlotCapacity":5,"actor":"evil"}`},
		{"wrong type", http.MethodPut, adminPath, `{"activeSlotCapacity":"5"}`},
		{"float for int", http.MethodPut, adminPath, `{"activeSlotCapacity":5.5}`},
		{"two objects", http.MethodPut, adminPath, `{"activeSlotCapacity":5}{"activeSlotCapacity":6}`},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			f := newAdminFixture(t)
			f.allowAdmin()
			f.get.EXPECT().Get(gomock.Any(), gomock.Any()).Times(0)
			f.set.EXPECT().Set(gomock.Any(), gomock.Any()).Times(0)
			rec := doAdmin(f.mux, c.method, c.path, c.body, bearerGood)
			assert.Equal(t, http.StatusBadRequest, rec.Code)
			assert.Equal(t, "VALIDATION_ERROR", decodeEnv(t, rec).errCode())
		})
	}
}

func TestSettingsAdmin_DomainErrorsMapToStatus(t *testing.T) {
	cases := []struct {
		name     string
		err      error
		method   string
		wantCode int
		wantErr  string
	}{
		{"put validation", shared.ValidationErrors{{Field: "pollNearMs", Message: "terlalu besar"}}, http.MethodPut, 400, "VALIDATION_ERROR"},
		{"put rules locked", entity.ErrRulesLocked, http.MethodPut, 409, "EVENT_RULES_LOCKED"},
		{"put event not found", entity.ErrEventNotFound, http.MethodPut, 404, "NOT_FOUND"},
		{"put booking down", entity.ErrDependencyUnavailable, http.MethodPut, 503, "DEPENDENCY_UNAVAILABLE"},
		{"put internal", errors.New("audit insert failed"), http.MethodPut, 500, "INTERNAL_ERROR"},
		{"get not configured", entity.ErrSettingsNotFound, http.MethodGet, 404, "QUEUE_NOT_CONFIGURED"},
		{"get internal", errors.New("pg down"), http.MethodGet, 500, "INTERNAL_ERROR"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			f := newAdminFixture(t)
			f.allowAdmin()
			if c.method == http.MethodGet {
				f.get.EXPECT().Get(gomock.Any(), adminEvent).Return(dto.QueueSettings{}, c.err)
			} else {
				f.set.EXPECT().Set(gomock.Any(), gomock.Any()).Return(dto.QueueSettings{}, false, c.err)
			}
			rec := doAdmin(f.mux, c.method, adminPath, `{"activeSlotCapacity":5}`, bearerGood)
			assert.Equal(t, c.wantCode, rec.Code)
			assert.Equal(t, c.wantErr, decodeEnv(t, rec).errCode())
			assert.NotContains(t, rec.Body.String(), "audit insert failed", "internal detail is not leaked")
		})
	}
}

func TestSettingsAdmin_ValidationNamesTheFieldInTheMessage(t *testing.T) {
	f := newAdminFixture(t)
	f.allowAdmin()
	f.set.EXPECT().Set(gomock.Any(), gomock.Any()).Return(dto.QueueSettings{}, false,
		shared.ValidationErrors{{Field: "pollNearMs", Message: "terlalu besar"}})
	rec := doAdmin(f.mux, http.MethodPut, adminPath, `{"activeSlotCapacity":5}`, bearerGood)
	require.Equal(t, http.StatusBadRequest, rec.Code)
	e := decodeEnv(t, rec)
	assert.Equal(t, "VALIDATION_ERROR", e.errCode())
	assert.Contains(t, e.Meta.Message, "pollNearMs", "field yang salah disebut di pesan")
	assert.Contains(t, e.Meta.Message, "terlalu besar")
}
