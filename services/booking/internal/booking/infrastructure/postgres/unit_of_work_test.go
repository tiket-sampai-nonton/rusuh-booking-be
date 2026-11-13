package postgres

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	domevent "github.com/tiket-sampai-nonton/rusuh-booking-be/services/booking/internal/booking/domain/event"
	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/booking/internal/booking/port/outbound"
)

func TestNewUnitOfWork(t *testing.T) {
	u, err := NewUnitOfWork(nil)
	require.NoError(t, err)
	assert.NotNil(t, u.store)
}

// Metadata event selalu membawa correlationId dan actor booking-service.
func TestMeta(t *testing.T) {
	assert.Equal(t, map[string]string{"correlationId": "c-1", "actor": "booking-service"}, meta("c-1"))
}

// Snapshot item di payload event hanya membawa kategori, harga, dan NIK tersamar (tanpa hash, ciphertext, nama).
func TestItemSnapshots(t *testing.T) {
	lines := []outbound.HoldLine{
		{CategoryID: "cat-a", UnitPriceMinor: 100, AttendeeNIKMasked: "32**", AttendeeNIKHash: []byte("h"), AttendeeName: "Peserta Satu"},
		{CategoryID: "cat-b", UnitPriceMinor: 200, AttendeeNIKMasked: "33**"},
	}
	assert.Equal(t, []domevent.ItemSnapshot{
		{CategoryID: "cat-a", UnitPriceMinor: 100, AttendeeNIKMasked: "32**"},
		{CategoryID: "cat-b", UnitPriceMinor: 200, AttendeeNIKMasked: "33**"},
	}, itemSnapshots(lines))
	assert.Empty(t, itemSnapshots(nil))
}

// clockOr memakai jam aplikasi dari spec; nilai nol jatuh ke jam dinding UTC.
func TestClockOr(t *testing.T) {
	fixed := time.Date(2026, 1, 1, 10, 0, 0, 0, time.UTC)
	assert.Equal(t, fixed, clockOr(fixed))

	before := time.Now().UTC()
	got := clockOr(time.Time{})
	assert.False(t, got.Before(before))
	assert.Equal(t, time.UTC, got.Location())
}
