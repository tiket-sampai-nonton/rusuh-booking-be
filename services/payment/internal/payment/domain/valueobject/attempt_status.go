package valueobject

// AttemptStatus is the lifecycle state of one payment attempt (migration 0001 CHECK).
type AttemptStatus string

// Attempt statuses in use. REFUND_REQUIRED and REFUNDED exist in the schema but are not used (refund is backlog).
const (
	AttemptCreated   AttemptStatus = "CREATED"
	AttemptPending   AttemptStatus = "PENDING"
	AttemptUnknown   AttemptStatus = "UNKNOWN"
	AttemptSucceeded AttemptStatus = "SUCCEEDED"
	AttemptFailed    AttemptStatus = "FAILED"
	AttemptCancelled AttemptStatus = "CANCELLED"
	AttemptExpired   AttemptStatus = "EXPIRED"
)

// OpenStatuses are the statuses that count as "open": at most one attempt per booking (uq_attempt_open_per_booking).
var OpenStatuses = []AttemptStatus{AttemptCreated, AttemptPending, AttemptUnknown}

// transitions is the state machine of design §4.1. SUCCEEDED is allowed from CANCELLED and EXPIRED because
// the gateway is the source of truth for money: a payment can settle just after a cancel or expiry.
// SUCCEEDED and FAILED never move again.
var transitions = map[AttemptStatus][]AttemptStatus{
	AttemptCreated:   {AttemptPending, AttemptUnknown, AttemptCancelled, AttemptFailed},
	AttemptUnknown:   {AttemptPending, AttemptSucceeded, AttemptFailed, AttemptCancelled, AttemptExpired},
	AttemptPending:   {AttemptSucceeded, AttemptFailed, AttemptCancelled, AttemptExpired},
	AttemptCancelled: {AttemptSucceeded},
	AttemptExpired:   {AttemptSucceeded},
}

// IsOpen reports whether the attempt is still waiting for the customer or the gateway.
func (s AttemptStatus) IsOpen() bool {
	return s == AttemptCreated || s == AttemptPending || s == AttemptUnknown
}

// CanTransitionTo reports whether the state machine allows s → to. A transition to the same status is not
// a transition (callers treat it as a duplicate).
func (s AttemptStatus) CanTransitionTo(to AttemptStatus) bool {
	for _, t := range transitions[s] {
		if t == to {
			return true
		}
	}
	return false
}

// SourcesOf returns every status from which to is reachable; used as the WHERE guard of a conditional UPDATE.
func SourcesOf(to AttemptStatus) []AttemptStatus {
	var out []AttemptStatus
	for from, tos := range transitions {
		for _, t := range tos {
			if t == to {
				out = append(out, from)
			}
		}
	}
	return out
}
