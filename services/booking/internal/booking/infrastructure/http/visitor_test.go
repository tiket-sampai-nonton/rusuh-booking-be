package http_test

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tiket-sampai-nonton/rusuh-booking-be/pkg/httpx"
	"github.com/tiket-sampai-nonton/rusuh-booking-be/pkg/token"

	bookinghttp "github.com/tiket-sampai-nonton/rusuh-booking-be/services/booking/internal/booking/infrastructure/http"
)

type scopedVerifier map[string]token.Claims

func (v scopedVerifier) Verify(tok string, _ time.Time) (token.Claims, error) {
	if c, ok := v[tok]; ok {
		return c, nil
	}
	return token.Claims{}, token.ErrSignature
}

func visitorOf(t *testing.T, v scopedVerifier, set func(*http.Request)) (string, int) {
	t.Helper()
	var got string
	h := bookinghttp.ResolveVisitor(v, time.Now)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = bookinghttp.VisitorID(r)
		w.WriteHeader(http.StatusNoContent)
	}))
	r := httptest.NewRequest(http.MethodGet, "/x", nil)
	set(r)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	return got, w.Code
}

func TestResolveVisitor_OnlyAVerifiedVisitorTokenIdentifiesTheCaller(t *testing.T) {
	v := scopedVerifier{
		"good":      {Subject: "visitor-1", Scope: "visitor"},
		"admission": {Subject: "entry-1", Scope: "admission"},
		"nosub":     {Scope: "visitor"},
	}

	id, code := visitorOf(t, v, func(r *http.Request) { r.Header.Set("X-Visitor-Token", "good") })
	assert.Equal(t, "visitor-1", id)
	assert.Equal(t, http.StatusNoContent, code)

	id, code = visitorOf(t, v, func(r *http.Request) { r.AddCookie(&http.Cookie{Name: "rb_visitor", Value: "good"}) })
	assert.Equal(t, "visitor-1", id, "cookie web")
	assert.Equal(t, http.StatusNoContent, code)

	// No token: passes through anonymously (public routes; session creation is authorised by the admission token).
	id, code = visitorOf(t, v, func(*http.Request) {})
	assert.Empty(t, id)
	assert.Equal(t, http.StatusNoContent, code)

	// Raw identifiers are not identities: the old X-Visitor-ID header and a made-up token value are ignored/rejected.
	id, code = visitorOf(t, v, func(r *http.Request) { r.Header.Set("X-Visitor-ID", "victim-uuid") })
	assert.Empty(t, id, "X-Visitor-ID tidak boleh menjadi identitas")
	assert.Equal(t, http.StatusNoContent, code)
	for _, tok := range []string{"victim-uuid", "admission", "nosub"} {
		id, code = visitorOf(t, v, func(r *http.Request) { r.Header.Set("X-Visitor-Token", tok) })
		assert.Empty(t, id, tok)
		require.Equal(t, http.StatusUnauthorized, code, tok)
	}
	_, code = visitorOf(t, nil, func(r *http.Request) { r.Header.Set("X-Visitor-Token", "good") })
	assert.Equal(t, http.StatusUnauthorized, code, "tanpa verifier, token ditolak (fail-closed)")
}

func serveVisitor(v scopedVerifier, method, path string, set func(*http.Request)) *httptest.ResponseRecorder {
	h := bookinghttp.ResolveVisitor(v, time.Now)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))
	r := httptest.NewRequest(method, path, nil)
	set(r)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	return w
}

// A stale rb_visitor cookie must not blank the public catalog: those routes never use the caller's identity.
func TestResolveVisitor_PublicCatalogIgnoresInvalidToken(t *testing.T) {
	v := scopedVerifier{}
	stale := func(r *http.Request) { r.AddCookie(&http.Cookie{Name: "rb_visitor", Value: "stale"}) }
	for _, path := range []string{"/api/v1/events", "/api/v1/events?sort=DATE_ASC", "/api/v1/events/facets", "/api/v1/events/3f1c0c1e"} {
		w := serveVisitor(v, http.MethodGet, path, stale)
		assert.Equal(t, http.StatusNoContent, w.Code, path)
		assert.Empty(t, w.Header().Values("Set-Cookie"), path)
	}
}

// Only the exact catalog reads are exempt: everything else with a bad token stays 401 (fail-closed).
func TestResolveVisitor_InvalidTokenStill401OutsideCatalog(t *testing.T) {
	v := scopedVerifier{}
	stale := func(r *http.Request) { r.AddCookie(&http.Cookie{Name: "rb_visitor", Value: "stale"}) }
	for _, c := range []struct{ method, path string }{
		{http.MethodPost, "/api/v1/events"},
		{http.MethodGet, "/api/v1/events/3f1c0c1e/queue-entries"},
		{http.MethodGet, "/api/v1/eventsxyz"},
		{http.MethodGet, "/api/v1/booking-sessions/current"},
		{http.MethodGet, "/api/v1/bookings/b1"},
	} {
		w := serveVisitor(v, c.method, c.path, stale)
		assert.Equal(t, http.StatusUnauthorized, w.Code, c.method+" "+c.path)
	}
}

type errVerifier struct{ err error }

func (v errVerifier) Verify(string, time.Time) (token.Claims, error) { return token.Claims{}, v.err }

func cookieReq(val string) func(*http.Request) {
	return func(r *http.Request) { r.AddCookie(&http.Cookie{Name: "rb_visitor", Value: val}) }
}

// Only an EXPIRED rb_visitor cookie is cleared, in the same 401, so the browser self-heals. SameSite=None+Secure lets
// the deletion land on a cross-site response too (web app on another site than the API).
func TestResolveVisitor_ExpiresOnlyAnExpiredCookie(t *testing.T) {
	h := bookinghttp.ResolveVisitor(errVerifier{token.ErrExpired}, time.Now)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	r := httptest.NewRequest(http.MethodGet, "/api/v1/booking-sessions/current", nil)
	cookieReq("expired")(r)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	require.Equal(t, http.StatusUnauthorized, w.Code)
	cs := w.Result().Cookies()
	require.Len(t, cs, 1)
	assert.Equal(t, "rb_visitor", cs[0].Name)
	assert.Empty(t, cs[0].Value)
	assert.Equal(t, "/", cs[0].Path)
	assert.Less(t, cs[0].MaxAge, 0, "Max-Age<0 menghapus cookie")
	assert.True(t, cs[0].HttpOnly)
	assert.True(t, cs[0].Secure)
	assert.Equal(t, http.SameSiteNoneMode, cs[0].SameSite)
}

// A bad signature or a non-visitor token may mean Booking's key is wrong or rotating, not that the cookie is dead:
// clearing it then would wipe every visitor's queue identity. Those stay a plain 401. So does a header token (no
// cookie) and a missing verifier.
func TestResolveVisitor_KeepsCookieUnlessExpired(t *testing.T) {
	cases := map[string]struct {
		v   httpx.TokenVerifier
		set func(*http.Request)
	}{
		"tanda tangan salah":       {errVerifier{token.ErrSignature}, cookieReq("x")},
		"bukan scope visitor":      {scopedVerifier{"adm": {Subject: "e1", Scope: "admission"}}, cookieReq("adm")},
		"token header kedaluwarsa": {errVerifier{token.ErrExpired}, func(r *http.Request) { r.Header.Set("X-Visitor-Token", "x") }},
		"tanpa verifier":           {nil, cookieReq("x")},
	}
	for name, c := range cases {
		h := bookinghttp.ResolveVisitor(c.v, time.Now)(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
		r := httptest.NewRequest(http.MethodGet, "/api/v1/booking-sessions/current", nil)
		c.set(r)
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		require.Equal(t, http.StatusUnauthorized, w.Code, name)
		assert.Empty(t, w.Header().Values("Set-Cookie"), name)
	}
}
