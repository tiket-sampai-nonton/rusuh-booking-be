package http_test

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

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
