package user

import (
	"context"
	"encoding/base64"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tiket-sampai-nonton/rusuh-booking-be/pkg/crypto"
	"github.com/tiket-sampai-nonton/rusuh-booking-be/pkg/httpx"
	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/booking/internal/booking/port/inbound"
)

const fakeNIK = "3174010101900001"

func testSealer(t *testing.T) *crypto.Sealer {
	t.Helper()
	s, err := crypto.NewSealer(make([]byte, 32))
	require.NoError(t, err)
	return s
}

// userServer answers like the User Service handler: the {meta,data} envelope written by httpx.WriteData.
func userServer(t *testing.T, status int, data any) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "/internal/v1/accounts/acc-1/attendee-nik-enc", r.URL.Path)
		assert.Equal(t, "Bearer tok", r.Header.Get("Authorization"))
		if status != http.StatusOK {
			w.WriteHeader(status)
			return
		}
		httpx.WriteData(w, r, http.StatusOK, data)
	}))
	t.Cleanup(srv.Close)
	return srv
}

func TestNIKClient_ReSealsNIKFromUserEnvelope(t *testing.T) {
	sealer := testSealer(t)
	userEnc, err := sealer.Seal([]byte(fakeNIK), []byte(aadUserNIK))
	require.NoError(t, err)
	srv := userServer(t, http.StatusOK, map[string]string{"nikEnc": base64.StdEncoding.EncodeToString(userEnc)})

	got, err := NewNIKClient(srv.URL, sealer).GetNIKEnc(context.Background(), "acc-1", "tok")
	require.NoError(t, err)
	plain, err := sealer.Open(got, []byte(aadBookingNIK))
	require.NoError(t, err, "hasil harus tersegel dengan AAD Booking")
	assert.Equal(t, fakeNIK, string(plain))
}

func TestNIKClient_Errors(t *testing.T) {
	sealer := testSealer(t)
	wrongAAD, err := sealer.Seal([]byte(fakeNIK), []byte("other.aad"))
	require.NoError(t, err)
	tests := []struct {
		name   string
		status int
		data   any
		want   error
	}{
		{"akun tanpa NIK", http.StatusNotFound, nil, inbound.ErrNIKNotLinked},
		{"token ditolak User", http.StatusUnauthorized, nil, inbound.ErrDependency},
		{"nikEnc kosong", http.StatusOK, map[string]string{"nikEnc": ""}, inbound.ErrDependency},
		{"bukan base64", http.StatusOK, map[string]string{"nikEnc": "%%%"}, inbound.ErrDependency},
		{"AAD tidak cocok", http.StatusOK, map[string]string{"nikEnc": base64.StdEncoding.EncodeToString(wrongAAD)}, inbound.ErrDependency},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			srv := userServer(t, tt.status, tt.data)
			_, err := NewNIKClient(srv.URL, sealer).GetNIKEnc(context.Background(), "acc-1", "tok")
			assert.ErrorIs(t, err, tt.want)
		})
	}
}
