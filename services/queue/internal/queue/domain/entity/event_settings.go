package entity

import "time"

// EventSettings mirrors the event_settings row (design §3.3). Per-event tuning is the source of truth.
type EventSettings struct {
	EventID                     string
	ActiveSlotCapacity          int
	NearFrontMultiplier         int
	StatusPublishIntervalMs     int
	PollFarMs                   int
	PollMidMs                   int
	PollNearMs                  int
	OneActivePerVisitor         bool
	OneActivePerEmail           bool
	CategoryLimitedThresholdPct int
	PreTurnNotifyEnabled        bool
	UpdatedAt                   time.Time
}
