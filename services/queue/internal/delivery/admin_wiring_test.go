package delivery

import (
	"crypto/ed25519"
	"crypto/x509"
	"encoding/pem"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	"github.com/tiket-sampai-nonton/rusuh-booking-be/pkg/token"

	mock_inbound "github.com/tiket-sampai-nonton/rusuh-booking-be/services/queue/_mock/queue/port/inbound"
	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/queue/internal/queue/domain/dto"
	queuehttp "github.com/tiket-sampai-nonton/rusuh-booking-be/services/queue/internal/queue/infrastructure/http"
)

const adminTestPath = "/internal/v1/admin/events/11111111-1111-1111-1111-111111111111/queue-settings"

// writeUserKey writes a User Service public key PEM and returns the matching signer.
func writeUserKey(t *testing.T) (string, *token.Signer) {
	t.Helper()
	pub, priv, err := ed25519.GenerateKey(nil)
	require.NoError(t, err)
	der, err := x509.MarshalPKIXPublicKey(pub)
	require.NoError(t, err)
	path := filepath.Join(t.TempDir(), "user.pub.pem")
	require.NoError(t, os.WriteFile(path, pem.EncodeToMemory(&pem.Block{Type: "PUBLIC KEY", Bytes: der}), 0o600))
	signer, err := token.NewSigner(priv, "user-1")
	require.NoError(t, err)
	return path, signer
}

func userToken(t *testing.T, s *token.Signer, scope, email string) string {
	t.Helper()
	now := time.Now()
	tok, err := s.Sign(token.Claims{
		Issuer: "rbt-user", Audience: "rbt", Subject: "acct-1", Scope: scope, Email: email,
		IssuedAt: now.Add(-time.Minute).Unix(), ExpiresAt: now.Add(time.Hour).Unix(),
	})
	require.NoError(t, err)
	return tok
}

// QSA-5.2 / OQ-D: no User Service key -> no admin verifier at all (fail closed). The queue's own key must never be
// used as a fallback for admin tokens.
func TestAdminVerifier_DisabledWithoutUserPublicKey(t *testing.T) {
	cfg := testConfig(t)
	cfg.Token.PublicKeyFile = ""
	cfg.Admin.Emails = []string{"admin@contoh.id"}
	v, err := newAdminVerifier(cfg, time.Now, nil)
	require.NoError(t, err)
	assert.Nil(t, v, "must be an untyped nil interface so the handler's nil check denies all")

	ctrl := gomock.NewController(t)
	mux := http.NewServeMux()
	queuehttp.NewSettingsAdminHandler(mock_inbound.NewMockIGetQueueSettingsUsecase(ctrl), mock_inbound.NewMockISetQueueSettingsUsecase(ctrl), v).Register(mux)
	for _, m := range []string{http.MethodGet, http.MethodPut} {
		req := httptest.NewRequest(m, adminTestPath, nil)
		req.Header.Set("Authorization", "Bearer anything")
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, req)
		assert.Equal(t, http.StatusUnauthorized, rec.Code, m)
	}
}

func TestAdminVerifier_UsesUserKeyScopeAndAllowList(t *testing.T) {
	cfg := testConfig(t)
	path, signer := writeUserKey(t)
	cfg.Token.PublicKeyFile = path
	cfg.Admin.Emails = []string{"admin@contoh.id"}
	v, err := newAdminVerifier(cfg, time.Now, nil)
	require.NoError(t, err)
	require.NotNil(t, v)

	sub, err := v.VerifyAdmin(userToken(t, signer, "buyer", "admin@contoh.id"))
	require.NoError(t, err)
	assert.Equal(t, "acct-1", sub)
	_, err = v.VerifyAdmin(userToken(t, signer, "buyer", "orang@contoh.id"))
	assert.Error(t, err, "not allow-listed")
	_, err = v.VerifyAdmin(userToken(t, signer, "visitor", "admin@contoh.id"))
	assert.Error(t, err, "wrong scope")
}

func TestAdminVerifier_EmptyAllowListAdmitsNobody(t *testing.T) {
	cfg := testConfig(t)
	path, signer := writeUserKey(t)
	cfg.Token.PublicKeyFile = path
	cfg.Admin.Emails = nil
	v, err := newAdminVerifier(cfg, time.Now, nil)
	require.NoError(t, err)
	_, err = v.VerifyAdmin(userToken(t, signer, "buyer", "admin@contoh.id"))
	assert.Error(t, err)
}

func TestAdminVerifier_BadKeyFileIsAStartError(t *testing.T) {
	cfg := testConfig(t)
	cfg.Token.PublicKeyFile = filepath.Join(t.TempDir(), "missing.pem")
	_, err := newAdminVerifier(cfg, time.Now, nil)
	assert.Error(t, err)
}

// The admin routes are part of the module's handler list, next to the visitor-facing ones.
func TestSettingsAdminHandlerRegistersBothRoutes(t *testing.T) {
	ctrl := gomock.NewController(t)
	get := mock_inbound.NewMockIGetQueueSettingsUsecase(ctrl)
	set := mock_inbound.NewMockISetQueueSettingsUsecase(ctrl)
	get.EXPECT().Get(gomock.Any(), gomock.Any()).Return(dto.QueueSettings{}, nil).Times(0)
	mux := http.NewServeMux()
	queuehttp.NewSettingsAdminHandler(get, set, nil).Register(mux)
	for _, m := range []string{http.MethodGet, http.MethodPut} {
		_, pattern := mux.Handler(httptest.NewRequest(m, adminTestPath, nil))
		assert.NotEmpty(t, pattern, m)
	}
	_, pattern := mux.Handler(httptest.NewRequest(http.MethodDelete, adminTestPath, nil))
	assert.Empty(t, pattern, "no DELETE or PATCH route")
}
