package usecase_test

import (
	"bytes"
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"

	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/booking/internal/booking/application/usecase"
	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/booking/internal/booking/port/inbound"
	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/booking/internal/booking/port/outbound"
)

func TestStreamSession_EmitsAvailabilityAndTimer(t *testing.T) {
	adm := activeAdmission(true)
	cache := &fakeAvailCache{
		snaps: []outbound.CategoryAvailabilitySnap{{CategoryID: catA, Available: 5}},
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
