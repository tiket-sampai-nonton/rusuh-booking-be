package dto

// EntryStatus is the 200 data of GET /api/v1/queue-entries/me (design §2.3).
// It carries no PII (no NIK/email) — only queue position information.
type EntryStatus struct {
	EntryID       string `json:"entryId"`
	Seq           int64  `json:"seq"`
	Status        string `json:"status"`
	ExactPosition int64  `json:"exactPosition"`
	Zone          string `json:"zone,omitempty"`
	ClaimDeadline string `json:"claimDeadline,omitempty"`
	Reason        string `json:"reason,omitempty"`
	Redirect      string `json:"redirect,omitempty"`
}
