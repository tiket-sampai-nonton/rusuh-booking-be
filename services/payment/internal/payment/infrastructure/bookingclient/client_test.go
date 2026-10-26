package bookingclient_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tiket-sampai-nonton/rusuh-booking-be/pkg/resilience"

	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/payment/internal/payment/infrastructure/bookingclient"
	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/payment/internal/payment/port/outbound"
)

type fakeAuth struct{ err error }

func (f fakeAuth) Authorize(req *http.Request) error {
	if f.err != nil {
		return f.err
	}
	req.Header.Set("Authorization", "Bearer service-token")
	return nil
}

func newClient(t *testing.T, h http.Handler, auth bookingclient.Authorizer) *bookingclient.Client {
	t.Helper()
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	c, err := bookingclient.New(srv.URL, auth, nil, time.Now)
	require.NoError(t, err)
	return c
}

func envelope(w http.ResponseWriter, status int, data string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_, _ = w.Write([]byte(`{"meta":{"code":200},"data":` + data + `}`))
}

const payable = `{"bookingId":"b1","publicRef":"RB-7F3A9C21","status":"PENDING_PAYMENT","amountMinor":1500000,
 "paymentExpiresAt":"2026-10-26T10:10:00Z","visitorId":"v1","version":3,"canEdit":false,"buyerEmail":"x@y.id"}`

func TestGetPayable_SendsServiceTokenAndParsesEnvelope(t *testing.T) {
	var path, auth string
	c := newClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path, auth = r.Method+" "+r.URL.Path, r.Header.Get("Authorization")
		envelope(w, 200, payable)
	}), fakeAuth{})

	p, err := c.GetPayable(context.Background(), "b1")
	require.NoError(t, err)
	assert.Equal(t, "GET /internal/v1/bookings/b1/payable", path)
	assert.Equal(t, "Bearer service-token", auth)
	assert.Equal(t, outbound.Payable{
		BookingID: "b1", PublicRef: "RB-7F3A9C21", Status: "PENDING_PAYMENT", AmountMinor: 1500000,
		PaymentExpiresAt: time.Date(2026, 10, 26, 10, 10, 0, 0, time.UTC), VisitorID: "v1", Version: 3,
	}, p)
}

func TestLock_PostsAttemptKeyAndParsesOutcome(t *testing.T) {
	var got map[string]string
	c := newClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "POST /internal/v1/bookings/b1/payment-lock", r.Method+" "+r.URL.Path)
		_ = json.NewDecoder(r.Body).Decode(&got)
		envelope(w, 200, `{"outcome":"OK","version":4,"amountMinor":1500000,"paymentExpiresAt":"2026-10-26T10:10:00Z"}`)
	}), fakeAuth{})

	l, err := c.Lock(context.Background(), "b1", "idem-key-0123456789")
	require.NoError(t, err)
	assert.Equal(t, map[string]string{"attemptKey": "idem-key-0123456789"}, got)
	assert.Equal(t, outbound.LockOK, l.Outcome)
	assert.Equal(t, 4, l.Version)
	assert.EqualValues(t, 1500000, l.AmountMinor)

	c2 := newClient(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		envelope(w, 200, `{"outcome":"PAYMENT_EXPIRED"}`)
	}), fakeAuth{})
	l, err = c2.Lock(context.Background(), "b1", "idem-key-0123456789")
	require.NoError(t, err)
	assert.Equal(t, outbound.LockPaymentExpired, l.Outcome)
}

func TestErrors_NotFoundIsNotAnOutageButServerErrorsAre(t *testing.T) {
	c := newClient(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { envelope(w, 404, `null`) }), fakeAuth{})
	_, err := c.GetPayable(context.Background(), "nope")
	assert.ErrorIs(t, err, outbound.ErrBookingNotFound)

	for name, h := range map[string]http.HandlerFunc{
		"500":        func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(500) },
		"401 token":  func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(401) },
		"bukan JSON": func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte("<html>")) },
		"data rusak": func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte(`{"data":"x"}`)) },
	} {
		c := newClient(t, h, fakeAuth{})
		_, err := c.GetPayable(context.Background(), "b1")
		assert.ErrorIs(t, err, outbound.ErrBookingUnavailable, name)
	}

	c = newClient(t, http.NotFoundHandler(), fakeAuth{err: errors.New("kunci hilang")})
	_, err = c.GetPayable(context.Background(), "b1")
	assert.ErrorIs(t, err, outbound.ErrBookingUnavailable, "gagal menandatangani service token")
}

func TestBreaker_OpensOnServerErrorsOnly(t *testing.T) {
	var calls atomic.Int32
	var status atomic.Int32
	status.Store(404)
	c := newClient(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls.Add(1)
		w.WriteHeader(int(status.Load()))
	}), fakeAuth{})
	ctx := context.Background()

	for i := 0; i < 10; i++ {
		_, err := c.GetPayable(ctx, "b1")
		require.ErrorIs(t, err, outbound.ErrBookingNotFound)
	}
	assert.EqualValues(t, 10, calls.Load(), "404 bukan kegagalan breaker")

	status.Store(503)
	for i := 0; i < 5; i++ {
		_, err := c.GetPayable(ctx, "b1")
		require.ErrorIs(t, err, outbound.ErrBookingUnavailable)
	}
	before := calls.Load()
	_, err := c.GetPayable(ctx, "b1")
	assert.ErrorIs(t, err, outbound.ErrBookingUnavailable)
	assert.ErrorIs(t, err, resilience.ErrOpen)
	assert.Equal(t, before, calls.Load(), "breaker OPEN gagal cepat tanpa memanggil Booking")
}

func TestNew_RejectsBadURL(t *testing.T) {
	_, err := bookingclient.New("booking", fakeAuth{}, nil, time.Now)
	assert.Error(t, err)
}
