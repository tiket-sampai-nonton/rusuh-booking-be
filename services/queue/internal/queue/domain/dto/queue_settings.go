package dto

// QueueSettingsRequest is the PUT body. Every field is a pointer so "omitted" is distinguishable; a JSON null is
// treated as omitted. ActiveSlotCapacity is required (checked by the usecase).
type QueueSettingsRequest struct {
	ActiveSlotCapacity          *int  `json:"activeSlotCapacity"`
	NearFrontMultiplier         *int  `json:"nearFrontMultiplier"`
	StatusPublishIntervalMs     *int  `json:"statusPublishIntervalMs"`
	PollFarMs                   *int  `json:"pollFarMs"`
	PollMidMs                   *int  `json:"pollMidMs"`
	PollNearMs                  *int  `json:"pollNearMs"`
	OneActivePerVisitor         *bool `json:"oneActivePerVisitor"`
	OneActivePerEmail           *bool `json:"oneActivePerEmail"`
	CategoryLimitedThresholdPct *int  `json:"categoryLimitedThresholdPct"`
	PreTurnNotifyEnabled        *bool `json:"preTurnNotifyEnabled"`
}

// QueueSettings is the GET/PUT response.
type QueueSettings struct {
	EventID                     string `json:"eventId"`
	ActiveSlotCapacity          int    `json:"activeSlotCapacity"`
	NearFrontMultiplier         int    `json:"nearFrontMultiplier"`
	StatusPublishIntervalMs     int    `json:"statusPublishIntervalMs"`
	PollFarMs                   int    `json:"pollFarMs"`
	PollMidMs                   int    `json:"pollMidMs"`
	PollNearMs                  int    `json:"pollNearMs"`
	OneActivePerVisitor         bool   `json:"oneActivePerVisitor"`
	OneActivePerEmail           bool   `json:"oneActivePerEmail"`
	CategoryLimitedThresholdPct int    `json:"categoryLimitedThresholdPct"`
	PreTurnNotifyEnabled        bool   `json:"preTurnNotifyEnabled"`
	UpdatedAt                   string `json:"updatedAt"`
}
