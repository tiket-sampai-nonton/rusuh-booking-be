package http_test

import (
	"context"
	"crypto/ed25519"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tiket-sampai-nonton/rusuh-booking-be/pkg/token"

	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/booking/internal/booking/domain/dto"
	bookinghttp "github.com/tiket-sampai-nonton/rusuh-booking-be/services/booking/internal/booking/infrastructure/http"
)

type stubRelease struct{ got string }

func (s *stubRelease) Release(_ context.Context, id string) (dto.ReleaseResponse, error) {
	s.got = id
	return dto.ReleaseResponse{Released: true}, nil
}

// Task 4.R3: the contract path ".../{id}:release" is not a legal ServeMux pattern (the service panicked at start);
// registering it must not panic and the id must still reach the use case.
func TestInternalHandler_ReleaseAllocationRoute(t *testing.T) {
	queue := newIssuer(t, "queue-1")
	verifier, err := token.NewVerifier(map[string]ed25519.PublicKey{"queue-1": queue.pub}, "rbt-queue", "rbt", 5*time.Second)
	require.NoError(t, err)
	rel := &stubRelease{}
	mux := http.NewServeMux()
	require.NotPanics(t, func() {
		bookinghttp.NewInternalHandler(nil, userVerifier{verifier}).WithReleaseAllocation(rel).Register(mux)
	})
	tok := queue.token(t, token.Claims{Issuer: "rbt-queue", Subject: "rbt-queue", Audience: "rbt"})

	do := func(path string) int {
		req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(`{"reason":"QUEUE_ENTRY_GONE"}`))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Authorization", "Bearer "+tok)
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, req)
		return rec.Code
	}
	assert.Equal(t, http.StatusOK, do("/internal/v1/allocations/adm-1:release"))
	assert.Equal(t, "adm-1", rel.got)

	rel.got = ""
	assert.Equal(t, http.StatusNotFound, do("/internal/v1/allocations/adm-1"), "tanpa akhiran :release")
	assert.Equal(t, http.StatusNotFound, do("/internal/v1/allocations/:release"), "id kosong")
	assert.Empty(t, rel.got)
}
