package usecase

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"sync/atomic"
	"time"

	"github.com/tiket-sampai-nonton/rusuh-booking-be/pkg/clock"
	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/booking/internal/booking/port/inbound"
	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/booking/internal/booking/port/outbound"
)

// StreamSession implements IStreamSessionUsecase. It pushes availability + timer frames over SSE
// until the request context is cancelled (client disconnect), a frame write fails, or the session is
// past its submit grace.
type StreamSession struct {
	admissions outbound.IAdmissionRepo
	cache      outbound.IAvailabilityCache
	clock      clock.IClock
	heartbeat  time.Duration
	maxConn    int
	// conns counts the streams open in this process. The cap is per process, one API process per
	// container (D-C7.5), so it needs no shared store and cannot drift when a process dies.
	conns atomic.Int64
}

// NewStreamSession wires the SSE usecase. The connection cap applies per process (invariant: protect the backend).
func NewStreamSession(
	admissions outbound.IAdmissionRepo,
	cache outbound.IAvailabilityCache,
	clk clock.IClock,
	heartbeat time.Duration,
	maxConn int,
) *StreamSession {
	return &StreamSession{
		admissions: admissions,
		cache:      cache,
		clock:      clk,
		heartbeat:  heartbeat,
		maxConn:    maxConn,
	}
}

// Stream opens an SSE loop that emits availability + timer frames until ctx is cancelled, a frame
// write fails, or the session is past its submit grace (after one last timer frame). The session is
// verified once up front (owner + not expired); the connection cap is enforced per process, and the
// slot is released on every exit path.
func (uc *StreamSession) Stream(ctx context.Context, sessionID, visitorID string, w io.Writer, flush func()) error {
	// (0) Validate the path id up front: a malformed sessionId is a 400, not a 404 (REQ-4).
	if !isUUID(sessionID) {
		return inbound.Validation("sessionId", "sessionId harus UUID")
	}
	// (1) Verify session: it must exist, belong to the caller, and still be live.
	adm, err := uc.admissions.GetByID(ctx, sessionID)
	if err != nil {
		return err
	}
	if adm.VisitorID != visitorID {
		return inbound.ErrVisitorMismatch
	}
	if adm.IsExpired(uc.clock.Now()) {
		return inbound.ErrSessionExpired
	}

	// (2) Connection cap (per process). The slot is taken before the check so a refused stream releases it too.
	open := uc.conns.Add(1)
	defer uc.conns.Add(-1)
	if open > int64(uc.maxConn) {
		return inbound.ErrDependency
	}

	// (3) Emit loop: one availability + one timer frame per tick, flushing after each pair.
	ticker := time.NewTicker(uc.heartbeat)
	defer ticker.Stop()
	for {
		now := uc.clock.Now()

		// availability frame: a category shows what is free for everyone, plus this buyer's own reserved units
		// (already counted out of event free, and reserved for this buyer by invariant 2).
		snap, _ := uc.cache.GetAvailability(ctx, adm.EventID)
		cats := make([]outbound.CategoryAvailabilitySnap, 0, len(snap.Categories))
		for _, c := range snap.Categories {
			avail := snap.EventFree + adm.OutstandingUnits
			if c.Available < avail {
				avail = c.Available
			}
			cats = append(cats, outbound.CategoryAvailabilitySnap{CategoryID: c.CategoryID, Available: avail})
		}
		availData, _ := json.Marshal(map[string]any{
			"categories": cats,
			"serverTime": now.Format(time.RFC3339),
		})
		if _, err := fmt.Fprintf(w, "event: availability\ndata: %s\n\n", availData); err != nil {
			return err
		}

		// timer frame — phase is LOCKED once payment has been locked in, else SELECTING.
		phase := "SELECTING"
		if adm.LockedAt != nil {
			phase = "LOCKED"
		}
		timerData, _ := json.Marshal(map[string]any{
			"expiresAt":  adm.ExpiresAt.Format(time.RFC3339),
			"serverTime": now.Format(time.RFC3339),
			"phase":      phase,
		})
		if _, err := fmt.Fprintf(w, "event: timer\ndata: %s\n\n", timerData); err != nil {
			return err
		}

		flush()

		// Past the submit grace no hold is accepted any more: the timer frame just sent is the last one.
		if adm.PastGrace(now) {
			return nil
		}

		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
		}
	}
}
