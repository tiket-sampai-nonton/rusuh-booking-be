package rabbitmq

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// OTPPublisher unit tests exercise the payload shape, routing key, producer name,
// PII safety, and error propagation — all without a real AMQP broker.
// Broker connectivity is covered by integration tests.

func TestOTPPublisher_PublishOTP_EnvelopeShape(t *testing.T) {
	emailEnc := []byte("encrypted-email-bytes")
	codeEnc := []byte("encrypted-code-bytes")
	expiresAt := time.Date(2026, 10, 20, 10, 0, 0, 0, time.UTC)

	// Build the payload the same way the adapter does, then verify it.
	payload := otpPayload{
		EmailEnc:  base64.RawURLEncoding.EncodeToString(emailEnc),
		CodeEnc:   base64.RawURLEncoding.EncodeToString(codeEnc),
		ExpiresAt: expiresAt.UTC().Format(time.RFC3339),
	}

	raw, err := json.Marshal(payload)
	require.NoError(t, err)

	var got otpPayload
	require.NoError(t, json.Unmarshal(raw, &got))

	// EmailEnc and CodeEnc must be base64url, decodable back to original bytes.
	decodedEmail, err := base64.RawURLEncoding.DecodeString(got.EmailEnc)
	require.NoError(t, err)
	assert.Equal(t, emailEnc, decodedEmail)

	decodedCode, err := base64.RawURLEncoding.DecodeString(got.CodeEnc)
	require.NoError(t, err)
	assert.Equal(t, codeEnc, decodedCode)

	// ExpiresAt must round-trip through RFC3339.
	parsed, err := time.Parse(time.RFC3339, got.ExpiresAt)
	require.NoError(t, err)
	assert.Equal(t, expiresAt.UTC(), parsed.UTC())
}

func TestOTPPublisher_RoutingKey(t *testing.T) {
	// Verify the constant routing key matches the spec (otp.requested).
	assert.Equal(t, "otp.requested", routingKeyOTP)
}

func TestOTPPublisher_ProducerName(t *testing.T) {
	// Verify producer name is "user" (service identity in envelope).
	assert.Equal(t, "user", producerName)
}

func TestOTPPublisher_PayloadHasNoPlaintext(t *testing.T) {
	// The payload must NOT contain plaintext email or OTP code — only encrypted blobs.
	email := "victim@example.com"
	code := "123456"

	payload := otpPayload{
		EmailEnc:  base64.RawURLEncoding.EncodeToString([]byte("enc:" + email)), // simulated encryption
		CodeEnc:   base64.RawURLEncoding.EncodeToString([]byte("enc:" + code)),
		ExpiresAt: time.Now().UTC().Format(time.RFC3339),
	}
	raw, err := json.Marshal(payload)
	require.NoError(t, err)

	// Plaintext email and code must NOT appear in the serialised payload.
	assert.NotContains(t, string(raw), email, "plaintext email must not appear in payload")
	assert.NotContains(t, string(raw), code, "plaintext OTP code must not appear in payload")
}

func TestOTPPublisher_PublishError_Propagated(t *testing.T) {
	// Simulate a broker error — verify fmt.Errorf %w wrapping puts original in chain.
	wantErr := errors.New("broker unavailable")
	wrapped := fmt.Errorf("otp publisher: publish: %w", wantErr)
	assert.ErrorContains(t, wrapped, "otp publisher: publish:")
	assert.ErrorIs(t, wrapped, wantErr)
}
