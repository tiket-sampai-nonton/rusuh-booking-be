package resend_test

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tiket-sampai-nonton/rusuh-booking-be/pkg/rabbitmq"
	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/notification/internal/notification/infrastructure/resend"
)

const (
	testKey = "re_super_secret_key"
	testTo  = "buyer@example.com"
)

func newSender(t *testing.T, h http.HandlerFunc) *resend.Sender {
	t.Helper()
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	return resend.New(resend.Config{APIKey: testKey, From: "RBT <no-reply@rusuh.dev>", BaseURL: srv.URL, Timeout: 2 * time.Second})
}

func TestSendEmail_Success(t *testing.T) {
	var gotAuth, gotIdem, gotCT, gotPath, gotMethod string
	var gotBody map[string]any
	s := newSender(t, func(w http.ResponseWriter, r *http.Request) {
		gotAuth, gotIdem, gotCT = r.Header.Get("Authorization"), r.Header.Get("Idempotency-Key"), r.Header.Get("Content-Type")
		gotPath, gotMethod = r.URL.Path, r.Method
		raw, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(raw, &gotBody)
		_, _ = w.Write([]byte(`{"id":"49a3999c-0ce1-4ea6-ab68-afcd6dc2e794"}`))
	})

	id, err := s.SendEmail(context.Background(), testTo, "Subjek", "Isi pesan", "", "msg-1:BOOKING_PAID")

	require.NoError(t, err)
	assert.Equal(t, "49a3999c-0ce1-4ea6-ab68-afcd6dc2e794", id)
	assert.Equal(t, http.MethodPost, gotMethod)
	assert.Equal(t, "/emails", gotPath)
	assert.Equal(t, "Bearer "+testKey, gotAuth)
	assert.Equal(t, "msg-1:BOOKING_PAID", gotIdem)
	assert.Equal(t, "application/json", gotCT)
	assert.Equal(t, "RBT <no-reply@rusuh.dev>", gotBody["from"])
	assert.Equal(t, []any{testTo}, gotBody["to"])
	assert.Equal(t, "Subjek", gotBody["subject"])
	assert.Equal(t, "Isi pesan", gotBody["text"])
	assert.NotContains(t, gotBody, "html", "tanpa html, field html tidak dikirim")
}

func TestSendEmail_SendsHTMLWhenGiven(t *testing.T) {
	var gotBody map[string]any
	s := newSender(t, func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(raw, &gotBody)
		_, _ = w.Write([]byte(`{"id":"x"}`))
	})
	_, err := s.SendEmail(context.Background(), testTo, "Subjek", "Isi pesan", "<p>Isi</p>", "k")
	require.NoError(t, err)
	assert.Equal(t, "Isi pesan", gotBody["text"])
	assert.Equal(t, "<p>Isi</p>", gotBody["html"])
}

func TestSendEmail_ErrorClassification(t *testing.T) {
	tests := []struct {
		status    int
		permanent bool
	}{
		{http.StatusBadRequest, true},
		{http.StatusUnauthorized, true},
		{http.StatusForbidden, true},
		{http.StatusUnprocessableEntity, true},
		{http.StatusTooManyRequests, false},
		{http.StatusInternalServerError, false},
		{http.StatusBadGateway, false},
		{http.StatusServiceUnavailable, false},
		{http.StatusNotFound, false},
		{http.StatusConflict, false},
	}
	for _, tt := range tests {
		t.Run(http.StatusText(tt.status), func(t *testing.T) {
			s := newSender(t, func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(tt.status)
				_, _ = w.Write([]byte(`{"name":"validation_error","message":"x"}`))
			})
			_, err := s.SendEmail(context.Background(), testTo, "s", "b", "", "k")
			require.Error(t, err)
			assert.Equal(t, tt.permanent, rabbitmq.IsPermanent(err))
		})
	}
}

// The 403 message from Resend can contain the account owner's email address; it must never be
// passed on, nor may the API key or the recipient appear in any error (Property 7).
func TestSendEmail_ErrorNeverLeaksSecrets(t *testing.T) {
	s := newSender(t, func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusForbidden)
		_, _ = w.Write([]byte(`{"name":"validation_error","message":"You can only send testing emails to your own email address (owner@private.com). to=` + testTo + ` key=` + testKey + `"}`))
	})
	_, err := s.SendEmail(context.Background(), testTo, "s", "b", "", "k")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "403")
	assert.Contains(t, err.Error(), "validation_error")
	for _, secret := range []string{testKey, testTo, "owner@private.com", "own email address"} {
		assert.NotContains(t, err.Error(), secret)
	}
}

func TestSendEmail_ServerNameIsSanitized(t *testing.T) {
	s := newSender(t, func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte(`{"name":"boom owner@private.com"}`))
	})
	_, err := s.SendEmail(context.Background(), testTo, "s", "b", "", "k")
	require.Error(t, err)
	assert.NotContains(t, err.Error(), "owner@private.com")
}

func TestSendEmail_NetworkErrorIsTransient(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	url := srv.URL
	srv.Close() // nothing listens any more
	s := resend.New(resend.Config{APIKey: testKey, From: "no-reply@rusuh.dev", BaseURL: url, Timeout: time.Second})

	_, err := s.SendEmail(context.Background(), testTo, "s", "b", "", "k")

	require.Error(t, err)
	assert.False(t, rabbitmq.IsPermanent(err))
	assert.NotContains(t, err.Error(), testKey)
	assert.NotContains(t, err.Error(), testTo)
}

func TestSendEmail_TimeoutIsTransient(t *testing.T) {
	release := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { <-release }))
	t.Cleanup(func() { close(release); srv.Close() })
	s := resend.New(resend.Config{APIKey: testKey, From: "no-reply@rusuh.dev", BaseURL: srv.URL, Timeout: 50 * time.Millisecond})

	_, err := s.SendEmail(context.Background(), testTo, "s", "b", "", "k")

	require.Error(t, err)
	assert.False(t, rabbitmq.IsPermanent(err))
}

func TestSendEmail_SuccessWithoutIDIsAnError(t *testing.T) {
	s := newSender(t, func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte(`{}`)) })
	_, err := s.SendEmail(context.Background(), testTo, "s", "b", "", "k")
	require.Error(t, err)
	assert.False(t, rabbitmq.IsPermanent(err))
}
