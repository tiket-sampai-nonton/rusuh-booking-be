package dto

// StatusDocument is the public queue status served via CDN (design §2.2). No PII.
type StatusDocument struct {
	EventID               string          `json:"eventId"`
	SaleState             string          `json:"saleState"`
	ServingSeq            int64           `json:"servingSeq"`
	GuaranteedUntilSeq    int64           `json:"guaranteedUntilSeq"`
	PossibleUntilSeq      int64           `json:"possibleUntilSeq"`
	SoldOut               bool            `json:"soldOut"`
	RemainingTickets      int             `json:"remainingTickets"`
	AdmissionRatePerMin   int             `json:"admissionRatePerMin"`
	RemovedBuckets        *RemovedBuckets `json:"removedBuckets,omitempty"`
	NextPollAfterMs       int             `json:"nextPollAfterMs"`
	Poll                  Poll            `json:"poll"`
	PreTurnNotifyPosition int             `json:"preTurnNotifyPosition"`
	Categories            []CategoryState `json:"categories,omitempty"`
	UpdatedAt             string          `json:"updatedAt"`
}

// RemovedBuckets compensates positions for entries that left ahead (design §2.2).
type RemovedBuckets struct {
	Size    int     `json:"size"`
	FromSeq int64   `json:"fromSeq"`
	Counts  []int64 `json:"counts"`
}

// Poll tells clients how often to poll depending on their position (design §2.2).
type Poll struct {
	FarMs    int   `json:"farMs"`
	MidMs    int   `json:"midMs"`
	NearMs   int   `json:"nearMs"`
	FarAbove int64 `json:"farAbove"`
	MidAbove int64 `json:"midAbove"`
}

// CategoryState is one ticket category's availability (design §2.2).
type CategoryState struct {
	Code  string `json:"code"`
	Name  string `json:"name"`
	State string `json:"state"` // AVAILABLE | LIMITED | SOLD_OUT
}

// SaleState values.
const (
	SaleNotStarted = "NOT_STARTED"
	SaleOpen       = "OPEN"
	SaleClosed     = "CLOSED"
)

// Category state values.
const (
	CategoryAvailable = "AVAILABLE"
	CategoryLimited   = "LIMITED"
	CategorySoldOut   = "SOLD_OUT"
)
