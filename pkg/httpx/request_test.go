package httpx

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tiket-sampai-nonton/rusuh-booking-be/pkg/apperror"
)

type payload struct {
	Email string `json:"email"`
	N     int    `json:"n"`
}

func TestDecodeJSON(t *testing.T) {
	tests := []struct {
		name, body, ctype string
		max               int64
		wantStatus        int
		wantMsg           string
	}{
		{"ok", `{"email":"a@b.id","n":1}`, "application/json", 0, 0, ""},
		{"unknown field", `{"email":"a@b.id","x":1}`, "application/json", 0, 400, "unknown field"},
		{"syntax", `{"email":`, "application/json", 0, 400, "body JSON tidak valid"},
		{"type", `{"n":"x"}`, "application/json", 0, 400, "tipe field n"},
		{"empty", ``, "application/json", 0, 400, "body kosong"},
		{"two objects", `{"n":1}{"n":2}`, "application/json", 0, 400, "satu objek"},
		{"too large", `{"email":"` + strings.Repeat("a", 100) + `"}`, "application/json", 32, 413, "terlalu besar"},
		{"wrong content type", `{}`, "text/plain", 0, 415, "Content-Type"},
		{"bad value", `{"email":[]}`, "", 0, 400, "body JSON tidak valid"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(tt.body))
			if tt.ctype != "" {
				r.Header.Set("Content-Type", tt.ctype)
			}
			var p payload
			err := DecodeJSON(httptest.NewRecorder(), r, &p, tt.max)
			if tt.wantStatus == 0 {
				require.NoError(t, err)
				assert.Equal(t, "a@b.id", p.Email)
				return
			}
			ae := apperror.From(err)
			assert.Equal(t, tt.wantStatus, ae.HTTPStatus)
			assert.Contains(t, ae.Message, tt.wantMsg)
		})
	}
}

func TestClientIP(t *testing.T) {
	r := httptest.NewRequest(http.MethodGet, "/", nil)
	r.RemoteAddr = "10.0.1.5:4433"
	r.Header.Set("X-Forwarded-For", "6.6.6.6, 203.0.113.9")
	assert.Equal(t, "203.0.113.9", ClientIP(r, true))
	assert.Equal(t, "10.0.1.5", ClientIP(r, false))
	r.Header.Set("X-Forwarded-For", "not-an-ip")
	assert.Equal(t, "10.0.1.5", ClientIP(r, true))
	r.RemoteAddr = "weird"
	assert.Equal(t, "weird", ClientIP(r, false))
}

func TestBearerToken(t *testing.T) {
	r := httptest.NewRequest(http.MethodGet, "/", nil)
	_, ok := BearerToken(r)
	assert.False(t, ok)
	r.Header.Set("Authorization", "Basic abc")
	_, ok = BearerToken(r)
	assert.False(t, ok)
	r.Header.Set("Authorization", "bearer  tok123 ")
	tok, ok := BearerToken(r)
	assert.True(t, ok)
	assert.Equal(t, "tok123", tok)
}

func TestWriteData(t *testing.T) {
	rec := httptest.NewRecorder()
	h := Chain(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		WriteData(w, r, http.StatusCreated, map[string]string{"id": "x"})
	}), RequestID())
	r := httptest.NewRequest(http.MethodPost, "/", nil)
	r.Header.Set(HeaderRequestID, "req-9")
	h.ServeHTTP(rec, r)
	assert.Equal(t, http.StatusCreated, rec.Code)
	assert.JSONEq(t, `{"meta":{"code":201,"requestId":"req-9"},"data":{"id":"x"}}`, rec.Body.String())
}
