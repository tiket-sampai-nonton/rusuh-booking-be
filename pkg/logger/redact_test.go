package logger

import (
	"bytes"
	"encoding/json"
	"log/slog"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Nilai palsu: bukan NIK atau email sungguhan.
const secretValue = "VALUE-0000-FAKE"

var sensitiveKeys = []string{
	"nik", "buyerNik", "email", "emailConfirmation", "token", "authorization", "signature",
	"signature_key", "password", "code", "pushToken", "secret",
	// varian huruf dan pemisah
	"NIK", "Email", "buyer_nik", "signatureKey", "PASSWORD", "push-token",
	// akhiran
	"otp", "otpCode", "apiKey", "serverKey", "sessionId", "cookie", "phone",
	"accessToken", "refresh_token", "buyerEmail", "client_secret", "dbPassword", "userNik",
}

func newBuf(t *testing.T, format string) (*slog.Logger, *bytes.Buffer) {
	t.Helper()
	var buf bytes.Buffer
	l, err := New(&buf, Config{Level: "debug", Format: format}, "svc", "api")
	require.NoError(t, err)
	return l, &buf
}

// Property 6: PII never logged. Setiap kunci sensitif x format x kedalaman tidak membocorkan nilai.
func TestRedaction_NeverLeaksValue(t *testing.T) {
	for _, format := range []string{"json", "text"} {
		for _, key := range sensitiveKeys {
			t.Run(format+"/"+key, func(t *testing.T) {
				l, buf := newBuf(t, format)

				l.Info("flat", key, secretValue)
				l.With(key, secretValue).Info("with")
				l.Info("group", slog.Group("req", slog.Group("body", slog.String(key, secretValue))))
				l.WithGroup("a").WithGroup("b").Info("withgroup", key, secretValue)
				l.Info("map", "payload", map[string]any{"x": []any{map[string]any{key: secretValue}}})

				assert.NotContains(t, buf.String(), secretValue)
				assert.Contains(t, buf.String(), redacted)
			})
		}
	}
}

func TestRedaction_KeepsNonSensitiveKeys(t *testing.T) {
	l, buf := newBuf(t, "json")
	l.Info("ok", "messageId", "m-1", "routingKey", "booking.paid.v1", "errorCode", "E1", "nik_hash", "abc123", "statusCode", 502)

	var rec map[string]any
	require.NoError(t, json.Unmarshal(buf.Bytes(), &rec))
	assert.Equal(t, "m-1", rec["messageId"])
	assert.Equal(t, "booking.paid.v1", rec["routingKey"])
	assert.Equal(t, "E1", rec["errorCode"])
	assert.Equal(t, "abc123", rec["nik_hash"])
	assert.EqualValues(t, 502, rec["statusCode"])
	assert.Equal(t, "svc", rec["service"])
}

func TestRedaction_JSONShape(t *testing.T) {
	l, buf := newBuf(t, "json")
	l.Info("x", slog.Group("req", slog.String("email", secretValue), slog.String("path", "/v1/x")))

	var rec map[string]any
	require.NoError(t, json.Unmarshal(buf.Bytes(), &rec))
	assert.Equal(t, map[string]any{"email": redacted, "path": "/v1/x"}, rec["req"])
}

func TestRedact_Payload(t *testing.T) {
	in := map[string]any{
		"name":   "Budi",
		"nik":    secretValue,
		"items":  []any{map[string]any{"email": secretValue, "qty": 2}},
		"header": map[string]string{"Authorization": secretValue, "Accept": "json"},
	}
	out := Redact(in).(map[string]any)

	assert.Equal(t, redacted, out["nik"])
	assert.Equal(t, "Budi", out["name"])
	assert.Equal(t, map[string]any{"email": redacted, "qty": 2}, out["items"].([]any)[0])
	assert.Equal(t, map[string]string{"Authorization": redacted, "Accept": "json"}, out["header"])
	assert.Equal(t, secretValue, in["nik"], "input tidak boleh diubah")
}

func TestRedact_PassesThroughOtherTypes(t *testing.T) {
	assert.Equal(t, "x", Redact("x"))
	assert.Equal(t, 3, Redact(3))
	assert.Nil(t, Redact(nil))
}

func TestRedact_HeaderAndRows(t *testing.T) {
	h := map[string][]string{"Authorization": {secretValue}, "Accept": {"json"}}
	assert.Equal(t, map[string][]string{"Authorization": {redacted}, "Accept": {"json"}}, Redact(h))

	rows := []map[string]any{{"email": secretValue, "qty": 1}}
	assert.Equal(t, []map[string]any{{"email": redacted, "qty": 1}}, Redact(rows))
}
