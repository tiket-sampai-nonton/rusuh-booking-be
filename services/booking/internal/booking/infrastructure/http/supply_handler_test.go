package http_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/booking/internal/booking/domain/dto"
	bookinghttp "github.com/tiket-sampai-nonton/rusuh-booking-be/services/booking/internal/booking/infrastructure/http"
	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/booking/internal/booking/port/inbound"
)

type stubSupply struct {
	snap dto.SupplySnapshot
	err  error
}

func (s *stubSupply) GetSupply(_ context.Context, _ string) (dto.SupplySnapshot, error) {
	return s.snap, s.err
}

func TestInternalHandler_GetSupply_OK(t *testing.T) {
	snap := dto.SupplySnapshot{
		EventID:          "evt-1",
		RemainingTickets: 12,
		SoldOut:          false,
		Categories: []dto.SupplyCategory{
			{Code: "VIP", Name: "VIP", Sold: 0, Total: 7},
			{Code: "REG", Name: "Regular", Sold: 0, Total: 5},
		},
	}
	h := bookinghttp.NewInternalHandler(nil, allowAll{}).WithSupply(&stubSupply{snap: snap})

	mux := http.NewServeMux()
	h.Register(mux)

	req := httptest.NewRequest(http.MethodGet, "/internal/v1/events/evt-1/supply", nil)
	req.Header.Set("Authorization", "Bearer t")
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)

	var resp struct {
		Data dto.SupplySnapshot `json:"data"`
	}
	require.NoError(t, json.NewDecoder(w.Body).Decode(&resp))
	assert.Equal(t, 12, resp.Data.RemainingTickets)
	assert.Len(t, resp.Data.Categories, 2)
}

func TestInternalHandler_GetSupply_NotFound(t *testing.T) {
	h := bookinghttp.NewInternalHandler(nil, allowAll{}).WithSupply(&stubSupply{err: inbound.ErrNotFound})

	mux := http.NewServeMux()
	h.Register(mux)

	req := httptest.NewRequest(http.MethodGet, "/internal/v1/events/unknown/supply", nil)
	req.Header.Set("Authorization", "Bearer t")
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	assert.Equal(t, http.StatusNotFound, w.Code)
}

// allowAll stands in for a verifier that accepts the Queue's service token.
type allowAll struct{}

func (allowAll) VerifyAccess(string) (string, error) { return "queue-service", nil }

type denyAllVerifier struct{}

func (denyAllVerifier) VerifyAccess(string) (string, error) {
	return "", errors.New("bukan service token")
}

// Internal endpoints never run open: without a verifier, or with a token the verifier refuses, they answer 401.
func TestInternalHandler_Supply_RefusesWithoutAServiceToken(t *testing.T) {
	for name, h := range map[string]*bookinghttp.InternalHandler{
		"tanpa verifier":      bookinghttp.NewInternalHandler(nil, nil).WithSupply(&stubSupply{}),
		"token ditolak":       bookinghttp.NewInternalHandler(nil, denyAllVerifier{}).WithSupply(&stubSupply{}),
		"tanpa header bearer": bookinghttp.NewInternalHandler(nil, allowAll{}).WithSupply(&stubSupply{}),
	} {
		t.Run(name, func(t *testing.T) {
			mux := http.NewServeMux()
			h.Register(mux)
			req := httptest.NewRequest(http.MethodGet, "/internal/v1/events/evt-1/supply", nil)
			if name != "tanpa header bearer" {
				req.Header.Set("Authorization", "Bearer t")
			}
			w := httptest.NewRecorder()
			mux.ServeHTTP(w, req)
			assert.Equal(t, http.StatusUnauthorized, w.Code)
		})
	}
}
