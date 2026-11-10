package mapper

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/queue/internal/queue/domain/dto"
	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/queue/internal/queue/domain/entity"
)

func TestToQueueSettings(t *testing.T) {
	loc := time.FixedZone("WIB", 7*3600)
	s := entity.DefaultSettings("e1")
	s.ActiveSlotCapacity = 42
	s.UpdatedAt = time.Date(2026, 11, 6, 10, 0, 0, 0, loc)
	got := ToQueueSettings(s)
	assert.Equal(t, dto.QueueSettings{
		EventID: "e1", ActiveSlotCapacity: 42, NearFrontMultiplier: 2, StatusPublishIntervalMs: 1000,
		PollFarMs: 30000, PollMidMs: 10000, PollNearMs: 3000, PollFarAbove: 500, PollMidAbove: 100,
		OneActivePerVisitor: true, OneActivePerEmail: true,
		CategoryLimitedThresholdPct: 5, PreTurnNotifyEnabled: true, UpdatedAt: "2026-11-06T03:00:00Z",
	}, got)
}

func TestToPatch(t *testing.T) {
	t.Run("all nil", func(t *testing.T) {
		assert.Equal(t, entity.SettingsPatch{}, ToPatch(dto.QueueSettingsRequest{}))
	})
	t.Run("supplied values carried", func(t *testing.T) {
		c, n, f := 7, 1200, false
		p := ToPatch(dto.QueueSettingsRequest{ActiveSlotCapacity: &c, PollNearMs: &n, OneActivePerEmail: &f})
		require.NotNil(t, p.ActiveSlotCapacity)
		assert.Equal(t, 7, *p.ActiveSlotCapacity)
		assert.Equal(t, 1200, *p.PollNearMs)
		assert.False(t, *p.OneActivePerEmail)
		assert.Nil(t, p.PollFarMs)
		assert.Nil(t, p.OneActivePerVisitor)
	})
}

func TestQueueSettingsRequest_JSONNullIsOmitted(t *testing.T) {
	var r dto.QueueSettingsRequest
	require.NoError(t, json.Unmarshal([]byte(`{"activeSlotCapacity":3,"pollFarMs":null}`), &r))
	p := ToPatch(r)
	assert.Equal(t, 3, *p.ActiveSlotCapacity)
	assert.Nil(t, p.PollFarMs)
}
