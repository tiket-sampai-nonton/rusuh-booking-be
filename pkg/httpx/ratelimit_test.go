package httpx

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func hit(h http.Handler, path, remote string) *httptest.ResponseRecorder {
	r := httptest.NewRequest(http.MethodGet, path, nil)
	r.RemoteAddr = remote + ":1234"
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	return w
}

func TestRateLimit_LimitsPerKeyAndResetsAfterTheWindow(t *testing.T) {
	clock := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	ok := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNoContent) })
	h := RateLimit(2, time.Minute, IPKey(false, "/health"), func() time.Time { return clock })(ok)

	assert.Equal(t, 204, hit(h, "/x", "1.1.1.1").Code)
	assert.Equal(t, 204, hit(h, "/x", "1.1.1.1").Code)
	w := hit(h, "/x", "1.1.1.1")
	assert.Equal(t, 429, w.Code)
	assert.Equal(t, "61", w.Header().Get("Retry-After"))
	assert.Contains(t, w.Body.String(), "RATE_LIMITED")

	assert.Equal(t, 204, hit(h, "/x", "2.2.2.2").Code, "IP lain punya jatah sendiri")
	for i := 0; i < 5; i++ {
		assert.Equal(t, 204, hit(h, "/health", "1.1.1.1").Code, "path yang dikecualikan tidak dihitung")
	}

	clock = clock.Add(time.Minute)
	assert.Equal(t, 204, hit(h, "/x", "1.1.1.1").Code, "jendela baru")
}

func TestRateLimit_DisabledAndOnlyPath(t *testing.T) {
	ok := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNoContent) })
	off := RateLimit(0, time.Minute, IPKey(false), time.Now)(ok)
	for i := 0; i < 10; i++ {
		assert.Equal(t, 204, hit(off, "/x", "1.1.1.1").Code)
	}
	h := RateLimit(1, time.Minute, OnlyPathKey("/hook", false), time.Now)(ok)
	assert.Equal(t, 204, hit(h, "/hook", "1.1.1.1").Code)
	assert.Equal(t, 429, hit(h, "/hook", "1.1.1.1").Code)
	assert.Equal(t, 204, hit(h, "/other", "1.1.1.1").Code)
	assert.Equal(t, 204, hit(h, "/other", "1.1.1.1").Code)
}

func TestRateLimit_FullTableDropsExpiredThenFailsOpen(t *testing.T) {
	clock := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	ok := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNoContent) })
	i := 0
	key := func(*http.Request) string { i++; return "k" + time.Duration(i).String() }
	h := RateLimit(1, time.Minute, key, func() time.Time { return clock })(ok)
	for n := 0; n < maxLimiterKeys+5; n++ {
		assert.Equal(t, 204, hit(h, "/x", "1.1.1.1").Code)
	}
	clock = clock.Add(2 * time.Minute)
	assert.Equal(t, 204, hit(h, "/x", "1.1.1.1").Code, "jendela kedaluwarsa dibuang")
}
