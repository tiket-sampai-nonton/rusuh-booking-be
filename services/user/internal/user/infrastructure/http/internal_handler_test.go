package http

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	stdhttp "net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	mock_inbound "github.com/tiket-sampai-nonton/rusuh-booking-be/services/user/_mock/user/port/inbound"
	mock_outbound "github.com/tiket-sampai-nonton/rusuh-booking-be/services/user/_mock/user/port/outbound"
	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/user/internal/user/domain/entity"
)

const nikEncPath = "/internal/v1/accounts/acc-1/attendee-nik-enc"

func internalSetup(t *testing.T) (*mock_outbound.MockIAccountRepository, *mock_inbound.MockIAccessTokenVerifier, *stdhttp.ServeMux) {
	ctrl := gomock.NewController(t)
	repo := mock_outbound.NewMockIAccountRepository(ctrl)
	verifier := mock_inbound.NewMockIAccessTokenVerifier(ctrl)
	mux := stdhttp.NewServeMux()
	NewInternalHandler(repo, verifier).Register(mux)
	return repo, verifier, mux
}

func getNIKEnc(mux *stdhttp.ServeMux, auth string) *httptest.ResponseRecorder {
	r := httptest.NewRequest(stdhttp.MethodGet, nikEncPath, nil)
	if auth != "" {
		r.Header.Set("Authorization", auth)
	}
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, r)
	return rec
}

func TestInternalHandler_GetNIKEnc_ReturnsOwnCiphertext(t *testing.T) {
	repo, verifier, mux := internalSetup(t)
	verifier.EXPECT().VerifyAccess("tok").Return("acc-1", nil)
	repo.EXPECT().GetNIKEncByID(gomock.Any(), "acc-1").Return([]byte("sealed"), nil)

	rec := getNIKEnc(mux, "Bearer tok")
	require.Equal(t, stdhttp.StatusOK, rec.Code, rec.Body.String())
	assert.Equal(t, "no-store", rec.Header().Get("Cache-Control"))
	var body struct {
		Data struct {
			NikEnc string `json:"nikEnc"`
		} `json:"data"`
	}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body))
	assert.Equal(t, base64.StdEncoding.EncodeToString([]byte("sealed")), body.Data.NikEnc)
}

func TestInternalHandler_GetNIKEnc_Refusals(t *testing.T) {
	tests := []struct {
		name   string
		auth   string
		mock   func(*mock_outbound.MockIAccountRepository, *mock_inbound.MockIAccessTokenVerifier)
		status int
		header string
	}{
		{"tanpa bearer", "", func(*mock_outbound.MockIAccountRepository, *mock_inbound.MockIAccessTokenVerifier) {}, 401, `Bearer realm="rbt"`},
		{"token tidak valid", "Bearer bad", func(_ *mock_outbound.MockIAccountRepository, v *mock_inbound.MockIAccessTokenVerifier) {
			v.EXPECT().VerifyAccess("bad").Return("", errors.New("invalid"))
		}, 401, `Bearer realm="rbt", error="invalid_token"`},
		{"akun lain", "Bearer tok", func(_ *mock_outbound.MockIAccountRepository, v *mock_inbound.MockIAccessTokenVerifier) {
			v.EXPECT().VerifyAccess("tok").Return("acc-2", nil)
		}, 401, ""},
		{"akun tidak ada", "Bearer tok", func(r *mock_outbound.MockIAccountRepository, v *mock_inbound.MockIAccessTokenVerifier) {
			v.EXPECT().VerifyAccess("tok").Return("acc-1", nil)
			r.EXPECT().GetNIKEncByID(gomock.Any(), "acc-1").Return(nil, entity.ErrNotFound)
		}, 404, ""},
		{"belum ada NIK", "Bearer tok", func(r *mock_outbound.MockIAccountRepository, v *mock_inbound.MockIAccessTokenVerifier) {
			v.EXPECT().VerifyAccess("tok").Return("acc-1", nil)
			r.EXPECT().GetNIKEncByID(gomock.Any(), "acc-1").Return(nil, nil)
		}, 404, ""},
		{"database gagal", "Bearer tok", func(r *mock_outbound.MockIAccountRepository, v *mock_inbound.MockIAccessTokenVerifier) {
			v.EXPECT().VerifyAccess("tok").Return("acc-1", nil)
			r.EXPECT().GetNIKEncByID(gomock.Any(), "acc-1").Return(nil, errors.New("down"))
		}, 500, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			repo, verifier, mux := internalSetup(t)
			tt.mock(repo, verifier)
			rec := getNIKEnc(mux, tt.auth)
			assert.Equal(t, tt.status, rec.Code, rec.Body.String())
			if tt.header != "" {
				assert.Equal(t, tt.header, rec.Header().Get("WWW-Authenticate"))
			}
		})
	}
}
