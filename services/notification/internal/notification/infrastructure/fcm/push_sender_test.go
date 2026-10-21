package fcm_test

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/notification/internal/notification/infrastructure/fcm"
	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/notification/internal/notification/port/outbound"
)

// writeCreds creates a throwaway service-account JSON whose token_uri points at srvURL.
func writeCreds(t *testing.T, tokenURI string) string {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	require.NoError(t, err)
	der := x509.MarshalPKCS1PrivateKey(key)
	pemKey := string(pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: der}))

	creds := map[string]string{
		"client_email": "svc@test.iam.gserviceaccount.com",
		"private_key":  pemKey,
		"token_uri":    tokenURI,
		"project_id":   "test-project",
	}
	raw, err := json.Marshal(creds)
	require.NoError(t, err)
	path := filepath.Join(t.TempDir(), "creds.json")
	require.NoError(t, os.WriteFile(path, raw, 0o600))
	return path
}

func newSender(t *testing.T, handler http.HandlerFunc) (*fcm.Sender, *httptest.Server) {
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)
	// The token endpoint and the send endpoint both hit srv; token_uri is read from creds.
	creds := writeCreds(t, srv.URL+"/token")
	s, err := fcm.NewFromCredentialsFile("test-project", creds, srv.Client())
	require.NoError(t, err)
	return s.WithBaseURL(srv.URL), srv
}

func TestFCM_IsTokenInvalid(t *testing.T) {
	handler := func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/token") {
			_ = json.NewEncoder(w).Encode(map[string]any{"access_token": "tok", "expires_in": 3600})
			return
		}
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte(`{"error":{"status":"UNREGISTERED"}}`))
	}
	s, _ := newSender(t, handler)

	_, err := s.Send(context.Background(), outbound.PushMessage{Token: "bad", Title: "t", Body: "b"})
	require.Error(t, err)
	assert.True(t, s.IsTokenInvalid(err))
}

func TestFCM_SendSuccess(t *testing.T) {
	handler := func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/token") {
			_ = json.NewEncoder(w).Encode(map[string]any{"access_token": "tok", "expires_in": 3600})
			return
		}
		assert.Equal(t, "Bearer tok", r.Header.Get("Authorization"))
		_ = json.NewEncoder(w).Encode(map[string]string{"name": "projects/test/messages/1"})
	}
	s, _ := newSender(t, handler)

	id, err := s.Send(context.Background(), outbound.PushMessage{Token: "ok", Title: "t", Body: "b"})
	require.NoError(t, err)
	assert.Equal(t, "projects/test/messages/1", id)
}

func TestFCM_ServerErrorIsTransient(t *testing.T) {
	handler := func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/token") {
			_ = json.NewEncoder(w).Encode(map[string]any{"access_token": "tok", "expires_in": 3600})
			return
		}
		w.WriteHeader(http.StatusServiceUnavailable)
	}
	s, _ := newSender(t, handler)

	_, err := s.Send(context.Background(), outbound.PushMessage{Token: "x", Title: "t", Body: "b"})
	require.Error(t, err)
	assert.False(t, s.IsTokenInvalid(err)) // 503 → transient, retry
}
