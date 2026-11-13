package security_test

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/queue/internal/queue/infrastructure/security"
	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/queue/internal/queue/port/outbound"
)

func newVisitorPair(t *testing.T) (*security.TokenIssuer, *security.VisitorAuthenticator, *security.DeviceHasher) {
	t.Helper()
	signer, verifier := newSigner(t)
	iss := security.NewTokenIssuer(signer, "rbt-queue", "rbt", time.Minute, 6*time.Hour, time.Hour)
	dh, err := security.NewDeviceHasher(hashKey)
	require.NoError(t, err)
	return iss, security.NewVisitorAuthenticator(verifier, time.Now).WithDeviceHasher(dh), dh
}

// R01.4/R01.6: the visitor token carries {visitorId, gen, dh}; the device claim is an HMAC of X-Device-Id, never
// the raw value; a mismatching or missing device header makes the token invalid.
func TestVisitorToken_GenerationAndDeviceClaim(t *testing.T) {
	iss, auth, dh := newVisitorPair(t)
	devHash := dh.Hash("device-raw-id-123")
	assert.Len(t, devHash, 32, "16 bytes as hex")
	assert.NotContains(t, devHash, "device-raw-id-123")

	tok, err := iss.IssueVisitor(outbound.VisitorClaim{VisitorID: "v-1", Gen: 7, DeviceHash: devHash}, time.Now())
	require.NoError(t, err)
	assert.NotContains(t, tok, "device-raw-id-123")

	tests := []struct {
		name   string
		device string
		ok     bool
	}{
		{"matching device", "device-raw-id-123", true},
		{"other device", "device-other", false},
		{"no device header", "", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := httptest.NewRequest("GET", "/", nil)
			r.Header.Set("X-Visitor-Token", tok)
			if tt.device != "" {
				r.Header.Set("X-Device-Id", tt.device)
			}
			got, ok := auth.ResolveToken(r)
			assert.Equal(t, tt.ok, ok)
			if tt.ok {
				assert.Equal(t, "v-1", got.VisitorID)
				assert.EqualValues(t, 7, got.Gen)
				assert.Equal(t, devHash, got.DeviceHash)
			}
		})
	}
}

func TestVisitorToken_WithoutDeviceClaimAcceptsAnyDevice(t *testing.T) {
	iss, auth, _ := newVisitorPair(t)
	tok, err := iss.IssueVisitor(outbound.VisitorClaim{VisitorID: "v-2", Gen: 1}, time.Now())
	require.NoError(t, err)
	r := httptest.NewRequest("GET", "/", nil)
	r.Header.Set("X-Visitor-Token", tok)
	r.Header.Set("X-Device-Id", "whatever")
	got, ok := auth.ResolveToken(r)
	require.True(t, ok)
	assert.EqualValues(t, 1, got.Gen)
	id, ok := auth.Resolve(r)
	assert.True(t, ok)
	assert.Equal(t, "v-2", id)
}

func TestVisitorToken_ExpiredOrForgedIsInvalid(t *testing.T) {
	iss, auth, _ := newVisitorPair(t)
	old, err := iss.IssueVisitor(outbound.VisitorClaim{VisitorID: "v-3", Gen: 1}, time.Now().Add(-7*time.Hour))
	require.NoError(t, err)
	r := httptest.NewRequest("GET", "/", nil)
	r.Header.Set("X-Visitor-Token", old)
	_, ok := auth.ResolveToken(r)
	assert.False(t, ok, "expired")

	good, err := iss.IssueVisitor(outbound.VisitorClaim{VisitorID: "v-3", Gen: 1}, time.Now())
	require.NoError(t, err)
	parts := strings.Split(good, ".")
	r.Header.Set("X-Visitor-Token", parts[0]+"."+parts[1]+".AAAA")
	_, ok = auth.ResolveToken(r)
	assert.False(t, ok, "bad signature")
}

// C6.2 (P1-13): GuestScope is the device hash when X-Device-Id is sent, else a keyed hash of the client IP; it is
// empty without a hasher or without any identity, so the idempotency key is then ignored.
func TestVisitorAuthenticator_GuestScope(t *testing.T) {
	_, auth, dh := newVisitorPair(t)
	_, verifier := newSigner(t)
	noHasher := security.NewVisitorAuthenticator(verifier, time.Now)
	req := func(device string) *http.Request {
		r := httptest.NewRequest("POST", "/", nil)
		if device != "" {
			r.Header.Set("X-Device-Id", device)
		}
		return r
	}

	assert.Equal(t, dh.Hash("device-raw-id-123"), auth.GuestScope(req("device-raw-id-123"), "203.0.113.7"), "device wins over IP")

	ip := auth.GuestScope(req(""), "203.0.113.7")
	assert.Regexp(t, `^ip-[0-9a-f]{32}$`, ip)
	assert.NotContains(t, ip, "203.0.113.7")
	assert.Equal(t, ip, auth.GuestScope(req(""), "203.0.113.7"), "stable")
	assert.NotEqual(t, ip, auth.GuestScope(req(""), "203.0.113.8"), "per IP")
	assert.Empty(t, auth.GuestScope(req(""), ""), "no device and no IP: no scope")
	assert.Empty(t, noHasher.GuestScope(req("device-raw-id-123"), "203.0.113.7"), "no hasher: no scope")
}

func TestDeviceHasher_RequiresKeyAndIsKeyed(t *testing.T) {
	_, err := security.NewDeviceHasher(nil)
	assert.Error(t, err)
	a, _ := security.NewDeviceHasher(hashKey)
	b, _ := security.NewDeviceHasher(encKey)
	assert.NotEqual(t, a.Hash("d"), b.Hash("d"))
	assert.Equal(t, a.Hash("d"), a.Hash("d"))
	assert.Empty(t, a.Hash(""), "no header: no claim")
}
