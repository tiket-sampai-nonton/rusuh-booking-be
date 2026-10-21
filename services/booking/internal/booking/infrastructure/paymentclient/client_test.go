package paymentclient_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

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
