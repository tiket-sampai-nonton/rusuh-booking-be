package mapper

import (
	"time"

	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/queue/internal/queue/domain/dto"
	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/queue/internal/queue/domain/entity"
)

// ToQueueSettings maps the settings entity to its response DTO (updatedAt as RFC3339 UTC).
func ToQueueSettings(s entity.EventSettings) dto.QueueSettings {
	return dto.QueueSettings{
		EventID:                     s.EventID,
		ActiveSlotCapacity:          s.ActiveSlotCapacity,
		NearFrontMultiplier:         s.NearFrontMultiplier,
		StatusPublishIntervalMs:     s.StatusPublishIntervalMs,
		PollFarMs:                   s.PollFarMs,
		PollMidMs:                   s.PollMidMs,
		PollNearMs:                  s.PollNearMs,
		PollFarAbove:                s.PollFarAbove,
		PollMidAbove:                s.PollMidAbove,
		OneActivePerVisitor:         s.OneActivePerVisitor,
		OneActivePerEmail:           s.OneActivePerEmail,
		CategoryLimitedThresholdPct: s.CategoryLimitedThresholdPct,
		PreTurnNotifyEnabled:        s.PreTurnNotifyEnabled,
		UpdatedAt:                   s.UpdatedAt.UTC().Format(time.RFC3339),
	}
}

// ToPatch maps the PUT body to the domain patch (nil = not supplied).
func ToPatch(r dto.QueueSettingsRequest) entity.SettingsPatch {
	return entity.SettingsPatch{
		ActiveSlotCapacity:          r.ActiveSlotCapacity,
		NearFrontMultiplier:         r.NearFrontMultiplier,
		StatusPublishIntervalMs:     r.StatusPublishIntervalMs,
		PollFarMs:                   r.PollFarMs,
		PollMidMs:                   r.PollMidMs,
		PollNearMs:                  r.PollNearMs,
		PollFarAbove:                r.PollFarAbove,
		PollMidAbove:                r.PollMidAbove,
		OneActivePerVisitor:         r.OneActivePerVisitor,
		OneActivePerEmail:           r.OneActivePerEmail,
		CategoryLimitedThresholdPct: r.CategoryLimitedThresholdPct,
		PreTurnNotifyEnabled:        r.PreTurnNotifyEnabled,
	}
}
