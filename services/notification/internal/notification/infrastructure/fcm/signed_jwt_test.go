package fcm_test

import (
	"context"
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/notification/internal/notification/infrastructure/fcm"
	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/notification/internal/notification/port/outbound"
)

// What Google's OAuth endpoint receives must stay a JWT-bearer grant whose assertion is RS256 with the expected claims
// and a valid signature, and the access token must be reused instead of requested again for every push.
func TestFCM_TokenExchangeSendsAValidRS256AssertionAndCachesTheToken(t *testing.T) {
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	require.NoError(t, err)
	var tokenCalls atomic.Int32
	var form map[string][]string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/token") {
			tokenCalls.Add(1)
			require.NoError(t, r.ParseForm())
			form = r.PostForm
			_ = json.NewEncoder(w).Encode(map[string]any{"access_token": "tok", "token_type": "Bearer", "expires_in": 3600})
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]string{"name": "projects/p/messages/1"})
	}))
	defer srv.Close()

	creds := map[string]string{
		"client_email": "svc@proj.iam.gserviceaccount.com",
		"private_key":  string(pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(key)})),
		"token_uri":    srv.URL + "/token",
		"project_id":   "p",
	}
	raw, err := json.Marshal(creds)
	require.NoError(t, err)
	path := filepath.Join(t.TempDir(), "creds.json")
	require.NoError(t, os.WriteFile(path, raw, 0o600))
	s, err := fcm.NewFromCredentialsFile("p", path, srv.Client())
	require.NoError(t, err)
	s.WithBaseURL(srv.URL)

	before := time.Now().Add(-time.Minute)
	for i := 0; i < 2; i++ {
		_, err = s.Send(context.Background(), outbound.PushMessage{Token: "device", Title: "t", Body: "b"})
		require.NoError(t, err)
	}
	assert.EqualValues(t, 1, tokenCalls.Load(), "access token dipakai ulang, tidak diminta tiap push")

	assert.Equal(t, []string{"urn:ietf:params:oauth:grant-type:jwt-bearer"}, form["grant_type"])
	require.Len(t, form["assertion"], 1)
	parts := strings.Split(form["assertion"][0], ".")
	require.Len(t, parts, 3)
	decode := func(seg string) map[string]any {
		b, err := base64.RawURLEncoding.DecodeString(seg)
		require.NoError(t, err)
		var m map[string]any
		require.NoError(t, json.Unmarshal(b, &m))
		return m
	}
	assert.Equal(t, "RS256", decode(parts[0])["alg"])
	claims := decode(parts[1])
	assert.Equal(t, "svc@proj.iam.gserviceaccount.com", claims["iss"])
	assert.Equal(t, "https://www.googleapis.com/auth/firebase.messaging", claims["scope"])
	assert.Equal(t, srv.URL+"/token", claims["aud"])
	iat, exp := int64(claims["iat"].(float64)), int64(claims["exp"].(float64))
	assert.GreaterOrEqual(t, iat, before.Unix())
	assert.Equal(t, time.Hour, time.Duration(exp-iat)*time.Second)

	sig, err := base64.RawURLEncoding.DecodeString(parts[2])
	require.NoError(t, err)
	sum := sha256.Sum256([]byte(parts[0] + "." + parts[1]))
	assert.NoError(t, rsa.VerifyPKCS1v15(&key.PublicKey, crypto.SHA256, sum[:], sig), "tanda tangan RS256 valid")
}
