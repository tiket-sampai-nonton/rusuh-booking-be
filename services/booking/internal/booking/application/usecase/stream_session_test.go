package usecase_test

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tiket-sampai-nonton/rusuh-booking-be/pkg/apperror"
	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/booking/internal/booking/application/usecase"
	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/booking/internal/booking/domain/entity"
	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/booking/internal/booking/port/inbound"
	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/booking/internal/booking/port/outbound"
)

func TestStreamSession_EmitsAvailabilityAndTimer(t *testing.T) {
	adm := activeAdmission(true)
	cache := &fakeAvailCache{
		snap: outbound.AvailabilitySnapshot{EventFree: 5, Categories: []outbound.CategoryAvailabilitySnap{{CategoryID: catA, Available: 5}}},
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
				snap: outbound.AvailabilitySnapshot{EventFree: 0, Categories: []outbound.CategoryAvailabilitySnap{{CategoryID: catA, Available: 5}}},
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

// stepAdmissionRepo answers each GetByID with the next outstanding-units value (the last one repeats), as a session
// does once its buyer holds the reserved units: the first read sees the reservation, later reads see none.
type stepAdmissionRepo struct {
	*fakeAdmissionRepo
	outstanding []int
	reads       int
}

func (r *stepAdmissionRepo) GetByID(ctx context.Context, id string) (entity.Admission, error) {
	a, err := r.fakeAdmissionRepo.GetByID(ctx, id)
	if err != nil {
		return a, err
	}
	i := r.reads
	if i >= len(r.outstanding) {
		i = len(r.outstanding) - 1
	}
	r.reads++
	a.OutstandingUnits = r.outstanding[i]
	return a, nil
}

// availabilityFrames decodes every "event: availability" frame in an SSE stream, in order.
func availabilityFrames(t *testing.T, stream string) []availabilityFrame {
	t.Helper()
	const prefix = "event: availability\ndata: "
	var out []availabilityFrame
	rest := stream
	for {
		start := strings.Index(rest, prefix)
		if start == -1 {
			return out
		}
		body := rest[start+len(prefix):]
		end := strings.Index(body, "\n\n")
		require.NotEqual(t, -1, end, "availability frame is not terminated")
		var f availabilityFrame
		require.NoError(t, json.Unmarshal([]byte(body[:end]), &f))
		out = append(out, f)
		rest = body[end:]
	}
}

// C5.1: the buyer's own outstanding units are read again on every tick. After the buyer's hold (outstanding 0) the
// category frame must stop adding those units, instead of repeating the value read when the stream started.
func TestStream_OwnReservedUnitsFollowTheHold(t *testing.T) {
	adm := activeAdmission(true)
	adm.OutstandingUnits = 3
	repo := &stepAdmissionRepo{fakeAdmissionRepo: &fakeAdmissionRepo{admission: adm}, outstanding: []int{3, 0}}
	cache := &fakeAvailCache{
		snap: outbound.AvailabilitySnapshot{EventFree: 0, Categories: []outbound.CategoryAvailabilitySnap{{CategoryID: catA, Available: 5}}},
	}
	uc := usecase.NewStreamSession(repo, cache, fixedClock{t: time.Now()}, 20*time.Millisecond, 100)
	ctx, cancel := context.WithTimeout(context.Background(), 150*time.Millisecond)
	defer cancel()
	var buf bytes.Buffer
	require.NoError(t, uc.Stream(ctx, sessionID, visitor, &buf, func() {}))

	frames := availabilityFrames(t, buf.String())
	require.Greater(t, len(frames), 2, "the stream must tick several times")
	assert.Equal(t, 3, frames[0].Categories[0].Available, "the first frame shows the reserved units")
	assert.Equal(t, 0, frames[len(frames)-1].Categories[0].Available, "after the hold the stream must stop adding them")
}

func TestStreamSession_CapExceeded(t *testing.T) {
	adm := activeAdmission(true)
	uc := usecase.NewStreamSession(
		&fakeAdmissionRepo{admission: adm},
		&fakeAvailCache{},
		fixedClock{t: time.Now()},
		80*time.Millisecond,
		0, // maxConn=0: the first stream is already over the cap
	)
	err := uc.Stream(context.Background(), sessionID, visitor, &bytes.Buffer{}, func() {})
	assert.ErrorIs(t, err, inbound.ErrDependency)
}

func TestStreamSession_SessionExpired(t *testing.T) {
	adm := activeAdmission(true)
	adm.ExpiresAt = time.Now().Add(-time.Minute)
	cache := &fakeAvailCache{}
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
	cache := &fakeAvailCache{}
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
			uc := usecase.NewStreamSession(repo, &fakeAvailCache{}, fixedClock{t: time.Now()}, 80*time.Millisecond, 100)
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

// steppingClock reads first, except on its second call, which reads later. The usecase checks expiry on its first call and
// its first frame loop on the second, so the session starts live and is past its grace by the first frame.
type steppingClock struct {
	first, later time.Time
	calls        int
}

func (c *steppingClock) Now() time.Time {
	c.calls++
	if c.calls == 2 {
		return c.later
	}
	return c.first
}

// failingWriter rejects every write, as a client that has gone away does.
type failingWriter struct{ err error }

func (w failingWriter) Write([]byte) (int, error) { return 0, w.err }

// signalWriter reports the first write, so a test knows the stream holds its connection slot.
type signalWriter struct {
	wrote chan struct{}
	once  sync.Once
}

func (w *signalWriter) Write(p []byte) (int, error) {
	w.once.Do(func() { close(w.wrote) })
	return len(p), nil
}

// assertSlotFree checks that no connection is still counted: a new stream on the same usecase with a cap of 1 is admitted.
func assertSlotFree(t *testing.T, uc *usecase.StreamSession) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	assert.NoError(t, uc.Stream(ctx, sessionID, visitor, &bytes.Buffer{}, func() {}), "a leaked connection slot would refuse this stream")
}

// C7.5: a frame write that fails ends the stream at once (not at the next tick) and releases its connection slot.
func TestStream_ExitsOnWriteError(t *testing.T) {
	uc := usecase.NewStreamSession(&fakeAdmissionRepo{admission: activeAdmission(true)}, &fakeAvailCache{},
		fixedClock{t: time.Now()}, time.Hour, 1)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	start := time.Now()
	err := uc.Stream(ctx, sessionID, visitor, failingWriter{err: io.ErrClosedPipe}, func() {})

	assert.ErrorIs(t, err, io.ErrClosedPipe)
	assert.Less(t, time.Since(start), time.Second, "the stream must end on the failed write, not wait for the heartbeat")
	assertSlotFree(t, uc)
}

// C7.5: once the session is past its submit grace, the stream sends one last timer frame and ends by itself.
func TestStream_ExitsAfterSessionExpiry(t *testing.T) {
	base := time.Now()
	adm := activeAdmission(true)
	adm.ExpiresAt = base.Add(time.Minute)
	adm.CfgSubmitGraceSeconds = 30
	clk := &steppingClock{first: base, later: base.Add(time.Hour)}
	uc := usecase.NewStreamSession(&fakeAdmissionRepo{admission: adm}, &fakeAvailCache{}, clk, time.Hour, 1)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	var buf bytes.Buffer
	start := time.Now()
	require.NoError(t, uc.Stream(ctx, sessionID, visitor, &buf, func() {}))

	assert.Less(t, time.Since(start), time.Second, "the heartbeat is one hour: only the expiry can end this stream")
	assert.Contains(t, buf.String(), "event: timer")
	assertSlotFree(t, uc)
}

// C7.5: the connection cap is per process. With a cap of 1 a second concurrent stream is refused while the first runs,
// and once both have ended the slot is free again.
func TestStream_ConnectionCapPerInstance(t *testing.T) {
	uc := usecase.NewStreamSession(&fakeAdmissionRepo{admission: activeAdmission(true)}, &fakeAvailCache{},
		fixedClock{t: time.Now()}, 10*time.Millisecond, 1)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	w := &signalWriter{wrote: make(chan struct{})}
	first := make(chan error, 1)
	go func() { first <- uc.Stream(ctx, sessionID, visitor, w, func() {}) }()
	select {
	case <-w.wrote:
	case <-time.After(5 * time.Second):
		t.Fatal("first stream never wrote a frame")
	}

	refusedCtx, refusedCancel := context.WithTimeout(context.Background(), time.Second)
	defer refusedCancel()
	err := uc.Stream(refusedCtx, sessionID, visitor, &bytes.Buffer{}, func() {})
	assert.ErrorIs(t, err, inbound.ErrDependency, "second concurrent stream must be refused by the cap")

	cancel()
	select {
	case err := <-first:
		assert.NoError(t, err)
	case <-time.After(5 * time.Second):
		t.Fatal("first stream did not end after its client went away")
	}
	assertSlotFree(t, uc)
}
