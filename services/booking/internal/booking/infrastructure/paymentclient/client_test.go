package paymentclient_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tiket-sampai-nonton/rusuh-booking-be/pkg/httpx"
	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/booking/internal/booking/infrastructure/paymentclient"
	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/booking/internal/booking/port/inbound"
	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/booking/internal/booking/port/outbound"
)

type fakeAuth struct{ err error }

func (f fakeAuth) Authorize(req *http.Request) error {
	if f.err != nil {
		return f.err
	}
	req.Header.Set("Authorization", "Bearer service-token")
	return nil
}

func reply(w http.ResponseWriter, code int, body string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_, _ = w.Write([]byte(body))
}

// F16: the request carries the mode and the answer is read from outcome (internal-payment.yaml).
func TestResolve_SendsModeAndReadsOutcome(t *testing.T) {
	var gotPath, gotAuth string
	var gotBody map[string]string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath, gotAuth = r.Method+" "+r.URL.Path, r.Header.Get("Authorization")
		_ = json.NewDecoder(r.Body).Decode(&gotBody)
		reply(w, 200, `{"meta":{"code":200},"data":{"outcome":"SUCCEEDED","settlementTime":"2026-10-26T02:13:04Z","attemptId":"a1"}}`)
	}))
	defer srv.Close()

	res, err := paymentclient.New(srv.URL, fakeAuth{}).Resolve(context.Background(), "b1", outbound.ResolveCheckAndCancel)
	require.NoError(t, err)
	assert.Equal(t, "POST /internal/v1/payments/b1/resolve", gotPath)
	assert.Equal(t, "Bearer service-token", gotAuth)
	assert.Equal(t, map[string]string{"mode": "CHECK_AND_CANCEL"}, gotBody)
	assert.Equal(t, outbound.OutcomeSucceeded, res.Outcome)
	require.NotNil(t, res.SettlementTime)
	assert.True(t, res.SettlementTime.Equal(time.Date(2026, 10, 26, 2, 13, 4, 0, time.UTC)))
}

func TestResolve_AllOutcomes(t *testing.T) {
	for _, o := range []outbound.ResolveOutcome{outbound.OutcomeSucceeded, outbound.OutcomeNotPaid, outbound.OutcomeNoAttempt, outbound.OutcomeUnknown} {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			reply(w, 200, `{"data":{"outcome":"`+string(o)+`"}}`)
		}))
		res, err := paymentclient.New(srv.URL, fakeAuth{}).Resolve(context.Background(), "b1", outbound.ResolveCheck)
		srv.Close()
		require.NoError(t, err, o)
		assert.Equal(t, o, res.Outcome)
		assert.Nil(t, res.SettlementTime)
	}
}

// Anything but a clear answer keeps the stock held: the caller gets a dependency error, never "not paid".
func TestResolve_UnclearAnswersAreDependencyErrors(t *testing.T) {
	cases := map[string]http.HandlerFunc{
		"500":               func(w http.ResponseWriter, _ *http.Request) { reply(w, 500, `{}`) },
		"401 token ditolak": func(w http.ResponseWriter, _ *http.Request) { reply(w, 401, `{}`) },
		"404":               func(w http.ResponseWriter, _ *http.Request) { reply(w, 404, `{}`) },
		"bukan JSON":        func(w http.ResponseWriter, _ *http.Request) { reply(w, 200, `<html>`) },
		"outcome kosong":    func(w http.ResponseWriter, _ *http.Request) { reply(w, 200, `{"data":{}}`) },
		"outcome asing":     func(w http.ResponseWriter, _ *http.Request) { reply(w, 200, `{"data":{"outcome":"PAID"}}`) },
		"format lama":       func(w http.ResponseWriter, _ *http.Request) { reply(w, 200, `{"data":{"paid":false}}`) },
	}
	for name, h := range cases {
		t.Run(name, func(t *testing.T) {
			srv := httptest.NewServer(h)
			defer srv.Close()
			_, err := paymentclient.New(srv.URL, fakeAuth{}).Resolve(context.Background(), "b1", outbound.ResolveCheck)
			assert.ErrorIs(t, err, inbound.ErrDependency)
		})
	}

	srv := httptest.NewServer(http.NotFoundHandler())
	url := srv.URL
	srv.Close()
	_, err := paymentclient.New(url, fakeAuth{}).Resolve(context.Background(), "b1", outbound.ResolveCheck)
	assert.ErrorIs(t, err, inbound.ErrDependency, "Payment tidak terjangkau")

	srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		t.Error("tidak boleh dipanggil tanpa token")
		reply(w, 200, `{}`)
	}))
	defer srv.Close()
	_, err = paymentclient.New(srv.URL, fakeAuth{err: errors.New("kunci hilang")}).Resolve(context.Background(), "b1", outbound.ResolveCheck)
	assert.ErrorIs(t, err, inbound.ErrDependency, "gagal membuat service token")
}

func TestResolve_PropagatesRequestAndTrace(t *testing.T) {
	var got http.Header
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = r.Header.Clone()
		reply(w, 200, `{"data":{"outcome":"NOT_PAID"}}`)
	}))
	defer srv.Close()
	c := paymentclient.New(srv.URL, propagating{})

	httpx.RequestID()(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		_, err := c.Resolve(r.Context(), "b1", outbound.ResolveCheck)
		require.NoError(t, err)
	})).ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/", nil))
	assert.NotEmpty(t, got.Get(httpx.HeaderRequestID))
}

// propagating mimics httpx.ServiceTokenSource: bearer token plus request id and trace headers.
type propagating struct{}

func (propagating) Authorize(req *http.Request) error {
	req.Header.Set("Authorization", "Bearer service-token")
	httpx.Propagate(req.Context(), req)
	return nil
}
