package delivery

import (
	"crypto/ed25519"
	"crypto/rand"
	"crypto/x509"
	"encoding/pem"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/user/internal/config"
)

// writeTestKey writes a throwaway Ed25519 private key for config validation.
func writeTestKey(t *testing.T) string {
	t.Helper()
	_, priv, err := ed25519.GenerateKey(rand.Reader)
	require.NoError(t, err)
	der, err := x509.MarshalPKCS8PrivateKey(priv)
	require.NoError(t, err)
	p := filepath.Join(t.TempDir(), "user-token.pem")
	require.NoError(t, os.WriteFile(p, pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der}), 0o600))
	return p
}

// HRD-012.2: a bad key is an error, never silently ignored, and the message names the variable without key bytes.
func TestNewPII_InvalidKeyReturnsErrorWithoutKeyBytes(t *testing.T) {
	good := "000102030405060708090a0b0c0d0e0f101112131415161718191a1b1c1d1e1f"
	good2 := "101112131415161718191a1b1c1d1e1f000102030405060708090a0b0c0d0e0f"
	good3 := "202122232425262728292a2b2c2d2e2f000102030405060708090a0b0c0d0e0f"
	secretish := "zz0102030405060708090a0b0c0d0e0f101112131415161718191a1b1c1d1eqq"

	_, err := newPII(config.PII{EncKeyHex: good, HashKeyHex: good2, OTPKeyHex: good3})
	require.NoError(t, err)

	tests := []struct {
		name string
		p    config.PII
		want string
	}{
		{"enc", config.PII{EncKeyHex: secretish, HashKeyHex: good2, OTPKeyHex: good3}, "PII_ENC_KEY"},
		{"hash", config.PII{EncKeyHex: good, HashKeyHex: "00ff", OTPKeyHex: good3}, "PII_HASH_KEY"},
		{"otp", config.PII{EncKeyHex: good, HashKeyHex: good2, OTPKeyHex: ""}, "OTP_HASH_KEY"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := newPII(tt.p)
			require.Error(t, err)
			assert.ErrorContains(t, err, tt.want)
			for _, k := range []string{secretish, good, good2, good3} {
				assert.NotContains(t, err.Error(), k)
			}
			assert.NotContains(t, err.Error(), "zz", "error hex bawaan tidak boleh menyebut byte kunci")
		})
	}
}
