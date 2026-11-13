//go:build e2e

package helpers

import (
	"crypto/ed25519"
	"crypto/rand"
	"crypto/x509"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"os"
	"time"
)

// MintServiceToken signs a short-lived Payment -> Booking service token with the local Payment test key
// (E2E_PAYMENT_KEY_PATH, default deployments/compose/keys/payment-token.pem). Service tokens live at most 5 minutes
// (httpx.ServiceTokenMaxTTL), so a token fixed in the environment cannot last a whole run: sign one per call.
func MintServiceToken() (string, error) {
	path := os.Getenv("E2E_PAYMENT_KEY_PATH")
	if path == "" {
		path = "../../deployments/compose/keys/payment-token.pem"
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("baca kunci uji payment: %w", err)
	}
	blk, _ := pem.Decode(raw)
	if blk == nil {
		return "", fmt.Errorf("kunci uji payment bukan PEM")
	}
	k, err := x509.ParsePKCS8PrivateKey(blk.Bytes)
	if err != nil {
		return "", err
	}
	priv, ok := k.(ed25519.PrivateKey)
	if !ok {
		return "", fmt.Errorf("kunci uji payment bukan Ed25519")
	}
	jti := make([]byte, 8)
	_, _ = rand.Read(jti)
	now := time.Now()
	h, _ := json.Marshal(map[string]string{"alg": "EdDSA", "typ": "JWT", "kid": "payment-1"})
	p, _ := json.Marshal(map[string]any{"iss": "rbt-payment", "sub": "rbt-payment", "aud": "rbt-booking", "jti": hex.EncodeToString(jti),
		"iat": now.Unix(), "exp": now.Add(4 * time.Minute).Unix()})
	enc := base64.RawURLEncoding
	signing := enc.EncodeToString(h) + "." + enc.EncodeToString(p)
	return signing + "." + enc.EncodeToString(ed25519.Sign(priv, []byte(signing))), nil
}
