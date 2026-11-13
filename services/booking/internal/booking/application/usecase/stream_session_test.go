package usecase_test

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tiket-sampai-nonton/rusuh-booking-be/pkg/apperror"
	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/booking/internal/booking/application/usecase"
	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/booking/internal/booking/port/inbound"
	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/booking/internal/booking/port/outbound"
)

func TestStreamSession_EmitsAvailabilityAndTimer(t *testing.T) {
	adm := activeAdmission(true)
	cache := &fakeAvailCache{
		snap:  outbound.AvailabilitySnapshot{EventFree: 5, Categories: []outbound.CategoryAvailabilitySnap{{CategoryID: catA, Available: 5}}},
		connN: 1,
	}
	uc := usecase.NewStreamSession(
		&fakeAdmissionRepo{admission: adm},
		cache,
		fixedClock{t: time.Now()},
		80*time.Millisecond,
		100,
	)
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	var buf bytes.Buffer
	var flushed bool
	err := uc.Stream(ctx, sessionID, visitor, &buf, func() { flushed = true })
	assert.NoError(t, err)
	assert.Contains(t, buf.String(), "event: availability")
	assert.Contains(t, buf.String(), "event: timer")
	assert.True(t, flushed)
}

// C5.1: a category frame shows min(category free, event free + the buyer's own outstanding units); the buyer's reserved
// units are already counted out of event free, so they are added back for this buyer only.
func TestStream_AvailabilityAddsOwnReservedUnits(t *testing.T) {
	tests := []struct {
		name        string
		outstanding int
		want        int
	}{
		{name: "own reserved units show although event free is zero", outstanding: 3, want: 3},
		{name: "no reserved units and no event free shows zero", outstanding: 0, want: 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			adm := activeAdmission(true)
			adm.OutstandingUnits = tt.outstanding
			cache := &fakeAvailCache{
				snap:  outbound.AvailabilitySnapshot{EventFree: 0, Categories: []outbound.CategoryAvailabilitySnap{{CategoryID: catA, Available: 5}}},
				connN: 1,
			}
			uc := usecase.NewStreamSession(&fakeAdmissionRepo{admission: adm}, cache, fixedClock{t: time.Now()}, 80*time.Millisecond, 100)
			ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
			defer cancel()
			var buf bytes.Buffer
			require.NoError(t, uc.Stream(ctx, sessionID, visitor, &buf, func() {}))

			frame := firstAvailabilityFrame(t, buf.String())
			require.Len(t, frame.Categories, 1)
			assert.Equal(t, catA, frame.Categories[0].CategoryID)
			assert.Equal(t, tt.want, frame.Categories[0].Available)
		})
	}
}

type availabilityFrame struct {
	Categories []outbound.CategoryAvailabilitySnap `json:"categories"`
}

// firstAvailabilityFrame decodes the data line of the first "event: availability" frame in an SSE stream.
func firstAvailabilityFrame(t *testing.T, stream string) availabilityFrame {
	t.Helper()
	const prefix = "event: availability\ndata: "
	start := strings.Index(stream, prefix)
	require.NotEqual(t, -1, start, "stream has no availability frame")
	body := stream[start+len(prefix):]
	end := strings.Index(body, "\n\n")
	require.NotEqual(t, -1, end, "availability frame is not terminated")
	var f availabilityFrame
	require.NoError(t, json.Unmarshal([]byte(body[:end]), &f))
	return f
}

func TestStreamSession_CapExceeded(t *testing.T) {
	adm := activeAdmission(true)
	cache := &fakeAvailCache{connN: 101}
	uc := usecase.NewStreamSession(
		&fakeAdmissionRepo{admission: adm},
		cache,
		fixedClock{t: time.Now()},
		80*time.Millisecond,
		100, // maxConn=100, connN=101 → over cap
	)
	err := uc.Stream(context.Background(), sessionID, visitor, &bytes.Buffer{}, func() {})
	assert.ErrorIs(t, err, inbound.ErrDependency)
}

func TestStreamSession_SessionExpired(t *testing.T) {
	adm := activeAdmission(true)
	adm.ExpiresAt = time.Now().Add(-time.Minute)
	cache := &fakeAvailCache{connN: 1}
	uc := usecase.NewStreamSession(
		&fakeAdmissionRepo{admission: adm},
		cache,
		fixedClock{t: time.Now()},
		80*time.Millisecond,
		100,
	)
	err := uc.Stream(context.Background(), sessionID, visitor, &bytes.Buffer{}, func() {})
	assert.ErrorIs(t, err, inbound.ErrSessionExpired)
}

func TestStreamSession_VisitorMismatch(t *testing.T) {
	adm := activeAdmission(true)
	cache := &fakeAvailCache{connN: 1}
	uc := usecase.NewStreamSession(
		&fakeAdmissionRepo{admission: adm},
		cache,
		fixedClock{t: time.Now()},
		80*time.Millisecond,
		100,
	)
	err := uc.Stream(context.Background(), sessionID, "someone-else", &bytes.Buffer{}, func() {})
	assert.ErrorIs(t, err, inbound.ErrVisitorMismatch)
}

// REQ-4: a malformed sessionId is rejected with VALIDATION_ERROR (400) BEFORE the admission repo is touched;
// a well-formed-but-absent UUID still reaches the repo and surfaces 404.
func TestStreamSession_PathUUIDValidation(t *testing.T) {
	tests := []struct {
		name      string
		sessionID string
		wantField string
		repoErr   error
		wantErrIs error
	}{
		{name: "non-uuid rejected before repo", sessionID: "not-a-uuid", wantField: "sessionId"},
		{name: "valid-but-absent uuid reaches repo and 404s", sessionID: sessionID, repoErr: inbound.ErrNotFound, wantErrIs: inbound.ErrNotFound},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			repo := &fakeAdmissionRepo{getErr: tt.repoErr}
			uc := usecase.NewStreamSession(repo, &fakeAvailCache{connN: 1}, fixedClock{t: time.Now()}, 80*time.Millisecond, 100)
			err := uc.Stream(context.Background(), tt.sessionID, visitor, &bytes.Buffer{}, func() {})
			require.Error(t, err)
			if tt.wantField != "" {
				assert.Equal(t, apperror.CodeValidationFailed, codeOf(t, err))
				assert.Equal(t, tt.wantField, validationField(t, err))
				assert.Zero(t, repo.getCalls, "repo must not be called for a malformed id")
				return
			}
			assert.ErrorIs(t, err, tt.wantErrIs)
			assert.Positive(t, repo.getCalls, "valid uuid must reach the repo")
		})
	}
}
