package paymentclient_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tiket-sampai-nonton/rusuh-booking-be/pkg/httpx"
	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/booking/internal/booking/infrastructure/paymentclient"
)

func TestResolve_Paid(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, http.MethodPost, r.Method)
		_, _ = w.Write([]byte(`{"data":{"paid":true}}`))
	}))
	defer srv.Close()

	res, err := paymentclient.New(srv.URL, "tok").Resolve(context.Background(), "b1")
	require.NoError(t, err)
	assert.True(t, res.Paid)
}

func TestResolve_NotFoundMeansUnpaid(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()

	res, err := paymentclient.New(srv.URL, "").Resolve(context.Background(), "b1")
	require.NoError(t, err)
	assert.False(t, res.Paid)
}

func TestResolve_ServerErrorIsDependency(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	_, err := paymentclient.New(srv.URL, "").Resolve(context.Background(), "b1")
	assert.Error(t, err)
}

// X-Request-ID dan trace id permintaan masuk diteruskan ke Payment.
func TestResolve_PropagatesRequestAndTrace(t *testing.T) {
	var gotReqID, gotTraceparent string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotReqID, gotTraceparent = r.Header.Get("X-Request-ID"), r.Header.Get("Traceparent")
		_, _ = w.Write([]byte(`{"data":{"paid":false}}`))
	}))
	defer srv.Close()
	c := paymentclient.New(srv.URL, "tok")

	in := httptest.NewRequest(http.MethodGet, "/", nil)
	in.Header.Set("X-Request-ID", "req-8")
	in.Header.Set("Traceparent", "00-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-01")
	httpx.Chain(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		_, err := c.Resolve(r.Context(), "b1")
		require.NoError(t, err)
	}), httpx.RequestID()).ServeHTTP(httptest.NewRecorder(), in)

	assert.Equal(t, "req-8", gotReqID)
	assert.Regexp(t, `^00-4bf92f3577b34da6a3ce929d0e0e4736-[0-9a-f]{16}-01$`, gotTraceparent)
}
