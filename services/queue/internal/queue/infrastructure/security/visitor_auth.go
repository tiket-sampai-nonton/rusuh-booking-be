package security

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/tiket-sampai-nonton/rusuh-booking-be/pkg/token"

	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/queue/internal/queue/port/outbound"
)

const (
	cookieVisitor = "rb_visitor"
	headerDevice  = "X-Device-Id"
)

// DeviceHasher turns the client's X-Device-Id into the device claim: HMAC-SHA256 truncated to 16 bytes, hex. The raw
// id is never stored, logged or put in a token. It binds a token to one install/browser profile; it is NOT a bot
// control (owner decision 7).
type DeviceHasher struct{ key []byte }

// NewDeviceHasher builds the hasher from a 32-byte key.
func NewDeviceHasher(key []byte) (*DeviceHasher, error) {
	if len(key) != 32 {
		return nil, errors.New("security: kunci device hash harus 32 byte")
	}
	return &DeviceHasher{key: append([]byte(nil), key...)}, nil
}

// Hash returns the device claim for a raw device id ("" when no id was sent).
func (d *DeviceHasher) Hash(deviceID string) string {
	if deviceID == "" {
		return ""
	}
	m := hmac.New(sha256.New, d.key)
	m.Write([]byte("queue.device:" + deviceID))
	return hex.EncodeToString(m.Sum(nil)[:16])
}

// FromRequest hashes the request's X-Device-Id header.
func (d *DeviceHasher) FromRequest(r *http.Request) string {
	if d == nil {
		return ""
	}
	return d.Hash(strings.TrimSpace(r.Header.Get(headerDevice)))
}

// VisitorAuthenticator verifies the EdDSA visitor token from the rb_visitor cookie or X-Visitor-Token header. A token
// is VALID (R01.4) iff its signature verifies, it has not expired and its device claim (when present) matches the
// request's X-Device-Id. The generation check (rotation) happens server-side against the stored generation.
type VisitorAuthenticator struct {
	verifier *token.Verifier
	now      func() time.Time
	device   *DeviceHasher
}

// NewVisitorAuthenticator returns the authenticator (verifies tokens minted by this service's signer).
func NewVisitorAuthenticator(verifier *token.Verifier, now func() time.Time) *VisitorAuthenticator {
	return &VisitorAuthenticator{verifier: verifier, now: now}
}

// WithDeviceHasher enables the device-claim check and DeviceHash.
func (a *VisitorAuthenticator) WithDeviceHasher(d *DeviceHasher) *VisitorAuthenticator {
	a.device = d
	return a
}

// DeviceHash returns the device claim for this request ("" without hasher or header).
func (a *VisitorAuthenticator) DeviceHash(r *http.Request) string { return a.device.FromRequest(r) }

// GuestScope is the idempotency scope of a tokenless guest (C6.2, P1-13): the device hash when X-Device-Id is sent,
// else a keyed hash of the client IP ("ip-" + 32 hex). The raw IP never leaves this function. It is "" without a
// hasher or without any identity, and the Idempotency-Key is then ignored (no replay).
func (a *VisitorAuthenticator) GuestScope(r *http.Request, clientIP string) string {
	if a.device == nil {
		return ""
	}
	if dh := a.DeviceHash(r); dh != "" {
		return dh
	}
	if clientIP == "" {
		return ""
	}
	m := hmac.New(sha256.New, a.device.key)
	m.Write([]byte("queue.ip:" + clientIP))
	return "ip-" + hex.EncodeToString(m.Sum(nil))[:32]
}

// ResolveToken returns the verified claim and whether a valid visitor token was present.
func (a *VisitorAuthenticator) ResolveToken(r *http.Request) (outbound.VisitorClaim, bool) {
	raw := ""
	if c, err := r.Cookie(cookieVisitor); err == nil && c.Value != "" {
		raw = c.Value
	} else if h := r.Header.Get("X-Visitor-Token"); h != "" {
		raw = h
	}
	if raw == "" {
		return outbound.VisitorClaim{}, false
	}
	claims, err := a.verifier.Verify(raw, a.now())
	if err != nil || claims.Scope != ScopeVisitor || claims.Subject == "" {
		return outbound.VisitorClaim{}, false
	}
	c := outbound.VisitorClaim{VisitorID: claims.Subject}
	// Legacy tokens (no jti) carry generation 0 and no device claim; the server generation check rejects them.
	if genPart, dh, found := strings.Cut(claims.ID, "."); found {
		c.Gen, _ = strconv.ParseInt(genPart, 10, 64)
		c.DeviceHash = dh
	}
	if c.DeviceHash != "" {
		got := a.DeviceHash(r)
		if got == "" || !hmac.Equal([]byte(got), []byte(c.DeviceHash)) {
			return outbound.VisitorClaim{}, false // copied to another device
		}
	}
	return c, true
}

// Resolve returns the verified visitor id (claim, leave and other visitor-scoped routes).
func (a *VisitorAuthenticator) Resolve(r *http.Request) (string, bool) {
	c, ok := a.ResolveToken(r)
	return c.VisitorID, ok
}
