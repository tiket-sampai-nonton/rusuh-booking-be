package dto

// JoinRequest is the body of POST /api/v1/events/{eventId}/queue-entries (design §2.1).
type JoinRequest struct {
	BuyerNik          string `json:"buyerNik"`
	Email             string `json:"email"`
	EmailConfirmation string `json:"emailConfirmation"`
	ConsentNik        bool   `json:"consentNik"`
}

// QueueEntry is the 201 data of Join Queue (design §2.1). visitorToken is returned to mobile clients
// only; web clients receive it as an HttpOnly cookie set by the handler.
type QueueEntry struct {
	EntryID       string `json:"entryId"`
	Seq           int64  `json:"seq"`
	Status        string `json:"status"`
	QueueToken    string `json:"queueToken,omitempty"`
	VisitorToken  string `json:"visitorToken,omitempty"`
	StatusURL     string `json:"statusUrl,omitempty"`
	ClaimDeadline string `json:"claimDeadline,omitempty"`
	JoinedAt      string `json:"joinedAt"`
	ServerTime    string `json:"serverTime"`
}
