package dto

// JoinRequest is the body of POST /api/v1/events/{eventId}/queue-entries. The join is anonymous (ADR-030): no
// field is read, so a legacy client that still sends buyerNik/email/consent is tolerated and the values are
// neither stored nor logged.
type JoinRequest struct{}

// QueueEntry is the 201 data of Join Queue (design §2.1). visitorToken is returned to mobile clients
// only; web clients receive it as an HttpOnly cookie set by the handler.
type QueueEntry struct {
	EntryID string `json:"entryId"`
	Seq     int64  `json:"seq"`
	Status  string `json:"status"`
	// DEPRECATED (owner Q6): no endpoint accepts it; still issued for one release, then removed. Clients use the visitor token.
	QueueToken    string `json:"queueToken,omitempty"`
	VisitorToken  string `json:"visitorToken,omitempty"`
	StatusURL     string `json:"statusUrl,omitempty"`
	ClaimDeadline string `json:"claimDeadline,omitempty"`
	JoinedAt      string `json:"joinedAt"`
	// State adds the ADR-030 liveness states; Rank is the FIFO-by-seq order (ADR-030 Amendment).
	State      string `json:"state"`
	Rank       *int64 `json:"rank"`
	ServerTime string `json:"serverTime"`

	// Existing is true when the join returned an entry that was already active (HTTP 200, not 201).
	Existing bool `json:"-"`
}
