package httpx_test

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tiket-sampai-nonton/rusuh-booking-be/pkg/httpx"
	"github.com/tiket-sampai-nonton/rusuh-booking-be/pkg/token"
)

var t0 = time.Date(2026, 10, 26, 10, 0, 0, 0, time.UTC)

type keypair struct {
	signer   *token.Signer
	verifier func(issuer, audience string) *token.Verifier
}

func newKeys(t *testing.T, kid string) keypair {
	t.Helper()
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	require.NoError(t, err)
	signer, err := token.NewSigner(priv, kid)
	require.NoError(t, err)
	return keypair{signer: signer, verifier: func(issuer, audience string) *token.Verifier {
		v, err := token.NewVerifier(map[string]ed25519.PublicKey{kid: pub}, issuer, audience, 0)
		require.NoError(t, err)
		return v
	}}
}

func sign(t *testing.T, k keypair, c token.Claims) string {
	t.Helper()
	tok, err := k.signer.Sign(c)
	require.NoError(t, err)
	return tok
}

func visitorToken(t *testing.T, k keypair, exp time.Duration) string {
	return sign(t, k, token.Claims{Issuer: "rbt-queue", Subject: "visitor-1", Audience: "rbt",
		IssuedAt: t0.Unix(), ExpiresAt: t0.Add(exp).Unix(), NIKHash: "abc123", Email: "pembeli@contoh.id"})
}

func serve(mw httpx.Middleware, req *http.Request) (*httptest.ResponseRecorder, bool) {
	called := false
	h := mw(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { called = true; w.WriteHeader(http.StatusNoContent) }))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec, called
}

func now() time.Time { return t0.Add(time.Minute) }

// Property 5: Same auth for web and mobile — cookie dan header menghasilkan identitas yang sama.
func TestVisitorAuth_CookieAndHeaderGiveSameIdentity(t *testing.T) {
	k := newKeys(t, "queue-1")
	tok := visitorToken(t, k, time.Hour)
	var web, mobile httpx.Visitor
	capture := func(dst *httpx.Visitor) http.Handler {
		return http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) { *dst, _ = httpx.VisitorFrom(r.Context()) })
	}
	mw := httpx.VisitorAuth(k.verifier("rbt-queue", "rbt"), now)

	r1 := httptest.NewRequest(http.MethodGet, "/", nil)
	r1.AddCookie(&http.Cookie{Name: httpx.CookieVisitor, Value: tok})
	mw(capture(&web)).ServeHTTP(httptest.NewRecorder(), r1)

	r2 := httptest.NewRequest(http.MethodGet, "/", nil)
	r2.Header.Set(httpx.HeaderVisitorToken, tok)
	mw(capture(&mobile)).ServeHTTP(httptest.NewRecorder(), r2)

	assert.Equal(t, httpx.Visitor{ID: "visitor-1", BuyerNikHash: "abc123", Email: "pembeli@contoh.id"}, web)
	assert.Equal(t, web, mobile)
}

func TestVisitorAuth_Rejects(t *testing.T) {
	k := newKeys(t, "queue-1")
	other := newKeys(t, "queue-1")
	mw := httpx.VisitorAuth(k.verifier("rbt-queue", "rbt"), now)
	cases := map[string]string{
		"tanpa token":       "",
		"sampah":            "bukan-jwt",
		"kedaluwarsa":       visitorToken(t, k, 30*time.Second),
		"tanda tangan lain": visitorToken(t, other, time.Hour),
		"audience salah":    sign(t, k, token.Claims{Issuer: "rbt-queue", Subject: "v", Audience: "rbt-lain", IssuedAt: t0.Unix(), ExpiresAt: t0.Add(time.Hour).Unix()}),
		"issuer salah":      sign(t, k, token.Claims{Issuer: "rbt-user", Subject: "v", Audience: "rbt", IssuedAt: t0.Unix(), ExpiresAt: t0.Add(time.Hour).Unix()}),
		"subject kosong":    sign(t, k, token.Claims{Issuer: "rbt-queue", Audience: "rbt", IssuedAt: t0.Unix(), ExpiresAt: t0.Add(time.Hour).Unix()}),
	}
	for name, tok := range cases {
		t.Run(name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, "/", nil)
			if tok != "" {
				req.Header.Set(httpx.HeaderVisitorToken, tok)
			}
			rec, called := serve(mw, req)
			assert.False(t, called, "handler tidak boleh dipanggil")
			assert.Equal(t, http.StatusUnauthorized, rec.Code)
			assert.Contains(t, rec.Header().Get("WWW-Authenticate"), "Bearer")
			var body struct{ Meta struct{ Message string } }
			require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body))
			assert.Contains(t, body.Meta.Message, "UNAUTHORIZED")
		})
	}
}

func serviceToken(t *testing.T, k keypair, issuer, audience string, ttl time.Duration) string {
	return sign(t, k, token.Claims{Issuer: issuer, Subject: issuer, Audience: audience, IssuedAt: t0.Unix(), ExpiresAt: t0.Add(ttl).Unix()})
}

func serviceReq(tok string) *http.Request {
	req := httptest.NewRequest(http.MethodPost, "/internal/v1/x", nil)
	if tok != "" {
		req.Header.Set("Authorization", "Bearer "+tok)
	}
	return req
}

// Property 4: Internal API closed.
func TestServiceAuth_AcceptsKnownCallersOnly(t *testing.T) {
	payment, booking, attacker := newKeys(t, "p-1"), newKeys(t, "b-1"), newKeys(t, "p-1")
	mw := httpx.ServiceAuth(now, payment.verifier("rbt-payment", "rbt-booking"), booking.verifier("rbt-booking", "rbt-booking"))

	var caller httpx.ServiceCaller
	h := mw(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		caller, _ = httpx.ServiceFrom(r.Context())
		w.WriteHeader(204)
	}))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, serviceReq(serviceToken(t, payment, "rbt-payment", "rbt-booking", 5*time.Minute)))
	assert.Equal(t, http.StatusNoContent, rec.Code)
	assert.Equal(t, "rbt-payment", caller.Issuer)

	rejected := map[string]string{
		"tanpa token":                "",
		"penanda tangan tak dikenal": serviceToken(t, attacker, "rbt-payment", "rbt-booking", time.Minute),
		"issuer tak dikenal":         serviceToken(t, payment, "rbt-queue", "rbt-booking", time.Minute),
		"audience service lain":      serviceToken(t, payment, "rbt-payment", "rbt-notification", time.Minute),
		"umur > 5 menit":             serviceToken(t, payment, "rbt-payment", "rbt-booking", 6*time.Minute),
		"kedaluwarsa":                serviceToken(t, payment, "rbt-payment", "rbt-booking", 30*time.Second),
		"token visitor":              visitorToken(t, payment, time.Hour),
	}
	for name, tok := range rejected {
		t.Run(name, func(t *testing.T) {
			rec, called := serve(mw, serviceReq(tok))
			assert.False(t, called)
			assert.Equal(t, http.StatusUnauthorized, rec.Code)
			assert.Contains(t, rec.Header().Get("WWW-Authenticate"), "Bearer")
		})
	}
}

func TestServiceAuth_FailsClosedWithoutVerifiers(t *testing.T) {
	k := newKeys(t, "p-1")
	rec, called := serve(httpx.ServiceAuth(now), serviceReq(serviceToken(t, k, "rbt-payment", "rbt-booking", time.Minute)))
	assert.False(t, called)
	assert.Equal(t, http.StatusUnauthorized, rec.Code)
}

func TestServiceTokenSource_CachesAndRefreshesBeforeExpiry(t *testing.T) {
	k := newKeys(t, "p-1")
	clock := t0
	src := httpx.NewServiceTokenSource(k.signer, "rbt-payment", "rbt-booking", func() time.Time { return clock })

	first, err := src.Token()
	require.NoError(t, err)
	clock = t0.Add(4 * time.Minute) // sisa 60 s > 30 s
	again, err := src.Token()
	require.NoError(t, err)
	assert.Equal(t, first, again, "token di-cache, tidak menandatangani per request")

	clock = t0.Add(4*time.Minute + 40*time.Second) // sisa 20 s < 30 s
	renewed, err := src.Token()
	require.NoError(t, err)
	assert.NotEqual(t, first, renewed, "ditandatangani ulang sebelum kedaluwarsa")

	// Token yang diterbitkan lolos ServiceAuth (round trip) dan umurnya ≤ 5 menit.
	mw := httpx.ServiceAuth(func() time.Time { return clock }, k.verifier("rbt-payment", "rbt-booking"))
	rec, called := serve(mw, serviceReq(renewed))
	assert.True(t, called)
	assert.Equal(t, http.StatusNoContent, rec.Code)
}

func TestServiceTokenSource_AuthorizeSetsHeaders(t *testing.T) {
	k := newKeys(t, "p-1")
	src := httpx.NewServiceTokenSource(k.signer, "rbt-payment", "rbt-booking", func() time.Time { return t0 })

	var req *http.Request
	httpx.RequestID()(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		req = httptest.NewRequest(http.MethodGet, "http://booking/internal/v1/x", nil).WithContext(r.Context())
		require.NoError(t, src.Authorize(req))
	})).ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/", nil))

	assert.Contains(t, req.Header.Get("Authorization"), "Bearer ey")
	assert.NotEmpty(t, req.Header.Get(httpx.HeaderRequestID), "X-Request-ID diteruskan")
}

func TestServiceTokenSource_ConcurrentCallsShareOneToken(t *testing.T) {
	k := newKeys(t, "p-1")
	src := httpx.NewServiceTokenSource(k.signer, "rbt-payment", "rbt-booking", func() time.Time { return t0 })
	var wg sync.WaitGroup
	tokens := make([]string, 20)
	for i := range tokens {
		wg.Add(1)
		go func() { defer wg.Done(); tokens[i], _ = src.Token() }()
	}
	wg.Wait()
	for _, tok := range tokens {
		assert.Equal(t, tokens[0], tok)
	}
}
