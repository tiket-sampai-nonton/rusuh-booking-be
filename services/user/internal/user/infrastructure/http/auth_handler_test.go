package http

import (
	"encoding/json"
	"errors"
	stdhttp "net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	"github.com/tiket-sampai-nonton/rusuh-booking-be/pkg/apperror"

	mock_inbound "github.com/tiket-sampai-nonton/rusuh-booking-be/services/user/_mock/user/port/inbound"
	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/user/internal/user/domain/dto"
	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/user/internal/user/port/inbound"
)

type fixture struct {
	auth     *mock_inbound.MockIAuthUsecase
	profile  *mock_inbound.MockIProfileUsecase
	verifier *mock_inbound.MockIAccessTokenVerifier
	mux      *stdhttp.ServeMux
}

func setup(t *testing.T) fixture {
	ctrl := gomock.NewController(t)
	f := fixture{
		auth:     mock_inbound.NewMockIAuthUsecase(ctrl),
		profile:  mock_inbound.NewMockIProfileUsecase(ctrl),
		verifier: mock_inbound.NewMockIAccessTokenVerifier(ctrl),
		mux:      stdhttp.NewServeMux(),
	}
	NewAuthHandler(f.auth, f.profile, f.verifier, true).Register(f.mux)
	return f
}

func do(f fixture, method, path, body string, header map[string]string) *httptest.ResponseRecorder {
	r := httptest.NewRequest(method, path, strings.NewReader(body))
	r.Header.Set("Content-Type", "application/json")
	r.RemoteAddr = "10.0.0.1:1234"
	for k, v := range header {
		r.Header.Set(k, v)
	}
	rec := httptest.NewRecorder()
	f.mux.ServeHTTP(rec, r)
	return rec
}

func errCode(t *testing.T, rec *httptest.ResponseRecorder) apperror.Code {
	t.Helper()
	var env apperror.Envelope
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &env))
	return env.Meta.ErrorCode
}

func TestRequestOTP(t *testing.T) {
	f := setup(t)
	f.auth.EXPECT().RequestOTP(gomock.Any(), dto.RequestOTPRequest{Email: "a@b.id"}, inbound.ClientInfo{IP: "203.0.113.9", UserAgent: "ua"}).
		Return(dto.RequestOTPResponse{ExpiresInSeconds: 300, ResendAfterSeconds: 60}, nil)
	rec := do(f, "POST", "/api/v1/auth/otp/request", `{"email":"a@b.id"}`, map[string]string{"X-Forwarded-For": "203.0.113.9", "User-Agent": "ua"})
	assert.Equal(t, stdhttp.StatusAccepted, rec.Code)
	assert.Equal(t, "no-store", rec.Header().Get("Cache-Control"))
	assert.JSONEq(t, `{"meta":{"code":202},"data":{"expiresInSeconds":300,"resendAfterSeconds":60}}`, rec.Body.String())

	rec = do(f, "POST", "/api/v1/auth/otp/request", `{"email":1}`, nil)
	assert.Equal(t, stdhttp.StatusBadRequest, rec.Code)

	f.auth.EXPECT().RequestOTP(gomock.Any(), gomock.Any(), gomock.Any()).Return(dto.RequestOTPResponse{}, inbound.ErrOTPResendTooSoon)
	rec = do(f, "POST", "/api/v1/auth/otp/request", `{"email":"a@b.id"}`, nil)
	assert.Equal(t, stdhttp.StatusTooManyRequests, rec.Code)
	assert.Equal(t, inbound.CodeOTPResendTooSoon, errCode(t, rec))
}

func TestVerifyRefreshLogout(t *testing.T) {
	f := setup(t)
	tok := dto.TokenResponse{TokenType: "Bearer", AccessToken: "a", RefreshToken: "r"}

	f.auth.EXPECT().VerifyOTP(gomock.Any(), dto.VerifyOTPRequest{Email: "a@b.id", Code: "123456"}, gomock.Any()).Return(tok, nil)
	rec := do(f, "POST", "/api/v1/auth/otp/verify", `{"email":"a@b.id","code":"123456"}`, nil)
	assert.Equal(t, stdhttp.StatusOK, rec.Code)
	assert.Contains(t, rec.Body.String(), `"accessToken":"a"`)

	f.auth.EXPECT().VerifyOTP(gomock.Any(), gomock.Any(), gomock.Any()).Return(dto.TokenResponse{}, inbound.ErrOTPInvalid)
	rec = do(f, "POST", "/api/v1/auth/otp/verify", `{"email":"a@b.id","code":"000000"}`, nil)
	assert.Equal(t, stdhttp.StatusUnauthorized, rec.Code)
	assert.Equal(t, stdhttp.StatusBadRequest, do(f, "POST", "/api/v1/auth/otp/verify", `[]`, nil).Code)

	f.auth.EXPECT().Refresh(gomock.Any(), dto.RefreshRequest{RefreshToken: "r"}, gomock.Any()).Return(tok, nil)
	assert.Equal(t, stdhttp.StatusOK, do(f, "POST", "/api/v1/auth/refresh", `{"refreshToken":"r"}`, nil).Code)
	f.auth.EXPECT().Refresh(gomock.Any(), gomock.Any(), gomock.Any()).Return(dto.TokenResponse{}, inbound.ErrSessionInvalid)
	assert.Equal(t, stdhttp.StatusUnauthorized, do(f, "POST", "/api/v1/auth/refresh", `{"refreshToken":"x"}`, nil).Code)
	assert.Equal(t, stdhttp.StatusBadRequest, do(f, "POST", "/api/v1/auth/refresh", `{`, nil).Code)

	f.auth.EXPECT().Logout(gomock.Any(), dto.RefreshRequest{RefreshToken: "r"}, gomock.Any()).Return(nil)
	assert.Equal(t, stdhttp.StatusNoContent, do(f, "POST", "/api/v1/auth/logout", `{"refreshToken":"r"}`, nil).Code)
	f.auth.EXPECT().Logout(gomock.Any(), gomock.Any(), gomock.Any()).Return(apperror.Internal(errors.New("db")))
	assert.Equal(t, stdhttp.StatusInternalServerError, do(f, "POST", "/api/v1/auth/logout", `{"refreshToken":"r"}`, nil).Code)
	assert.Equal(t, stdhttp.StatusBadRequest, do(f, "POST", "/api/v1/auth/logout", `{"x":1}`, nil).Code)
}

func TestMe(t *testing.T) {
	f := setup(t)
	auth := map[string]string{"Authorization": "Bearer good"}

	rec := do(f, "GET", "/api/v1/me", "", nil)
	assert.Equal(t, stdhttp.StatusUnauthorized, rec.Code)
	assert.Contains(t, rec.Header().Get("WWW-Authenticate"), "Bearer")

	f.verifier.EXPECT().VerifyAccess("bad").Return("", inbound.ErrUnauthorized)
	rec = do(f, "GET", "/api/v1/me", "", map[string]string{"Authorization": "Bearer bad"})
	assert.Equal(t, stdhttp.StatusUnauthorized, rec.Code)
	assert.Contains(t, rec.Header().Get("WWW-Authenticate"), "invalid_token")

	f.verifier.EXPECT().VerifyAccess("good").Return("acc-1", nil).Times(5)
	f.profile.EXPECT().GetProfile(gomock.Any(), "acc-1").Return(dto.ProfileResponse{AccountID: "acc-1"}, nil)
	rec = do(f, "GET", "/api/v1/me", "", auth)
	assert.Equal(t, stdhttp.StatusOK, rec.Code)
	assert.Contains(t, rec.Body.String(), `"accountId":"acc-1"`)

	f.profile.EXPECT().GetProfile(gomock.Any(), "acc-1").Return(dto.ProfileResponse{}, inbound.ErrUnauthorized)
	assert.Equal(t, stdhttp.StatusUnauthorized, do(f, "GET", "/api/v1/me", "", auth).Code)

	name := "Arif"
	f.profile.EXPECT().UpdateProfile(gomock.Any(), "acc-1", dto.UpdateProfileRequest{FullName: &name}).Return(dto.ProfileResponse{AccountID: "acc-1", FullName: "Arif"}, nil)
	assert.Equal(t, stdhttp.StatusOK, do(f, "PATCH", "/api/v1/me", `{"fullName":"Arif"}`, auth).Code)

	f.profile.EXPECT().UpdateProfile(gomock.Any(), "acc-1", gomock.Any()).Return(dto.ProfileResponse{}, inbound.ErrNIKAlreadyLinked)
	rec = do(f, "PATCH", "/api/v1/me", `{"nik":"3174010101900001"}`, auth)
	assert.Equal(t, stdhttp.StatusConflict, rec.Code)
	assert.Equal(t, inbound.CodeNIKAlreadyLinked, errCode(t, rec))

	assert.Equal(t, stdhttp.StatusBadRequest, do(f, "PATCH", "/api/v1/me", `{"email":"x"}`, auth).Code)
}
