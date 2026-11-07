package dto

// EntryStatus is the 200 data of GET /api/v1/queue-entries/me (design §2.3, ADR-030). No PII.
type EntryStatus struct {
	EntryID string `json:"entryId"`
	Seq     int64  `json:"seq"`
	Status  string `json:"status"` // raw stored status
	// State is the client state: WAITING | STALE | ADMITTED | CLAIMED | ENDED.
	State string `json:"state"`
	// Rank is the admission order (strict FIFO by seq; ADR-030 Amendment); null once ENDED.
	Rank          *int64 `json:"rank"`
	ExactPosition int64  `json:"exactPosition"`
	// Zone is GUARANTEED | POSSIBLE | NOT_ENOUGH, computed on seq by the server; null while ENDED.
	Zone            *string `json:"zone"`
	NextHeartbeatMs int64   `json:"nextHeartbeatMs,omitempty"`
	ClaimDeadline   string  `json:"claimDeadline,omitempty"`
	// Reason explains an ENDED entry: LEFT | STALE_TIMEOUT | CLAIM_WINDOW_EXPIRED | PAGE_TIMER_EXPIRED | JOURNEY_ENDED.
	Reason   string `json:"reason,omitempty"`
	Redirect string `json:"redirect,omitempty"`
}

// HeartbeatStatus is the 200 data of POST /api/v1/queue-entries/me/heartbeat.
type HeartbeatStatus struct {
	EntryID         string `json:"entryId"`
	State           string `json:"state"`
	Rank            *int64 `json:"rank"`
	NextHeartbeatMs int64  `json:"nextHeartbeatMs"`
}
