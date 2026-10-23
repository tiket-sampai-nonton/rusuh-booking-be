package valueobject

// Status is the lifecycle status of a queue entry (design §5).
type Status string

// Entry statuses.
const (
	StatusWaiting   Status = "WAITING"
	StatusAdmitted  Status = "ADMITTED"
	StatusClaimed   Status = "CLAIMED"
	StatusMissed    Status = "MISSED"
	StatusLeft      Status = "LEFT"
	StatusSoldOut   Status = "SOLD_OUT"
	StatusClosed    Status = "CLOSED"
	StatusCompleted Status = "COMPLETED"
	StatusEnded     Status = "ENDED"
)

// IsTerminal reports whether the status has no outgoing transition.
func (s Status) IsTerminal() bool {
	switch s {
	case StatusMissed, StatusLeft, StatusSoldOut, StatusClosed, StatusCompleted, StatusEnded:
		return true
	default:
		return false
	}
}

// CanTransitionTo enforces the §5 state machine. Same-status is not a transition (false).
func (s Status) CanTransitionTo(to Status) bool {
	if s.IsTerminal() {
		return false
	}
	switch s {
	case StatusWaiting:
		switch to {
		case StatusAdmitted, StatusLeft, StatusSoldOut, StatusClosed:
			return true
		}
	case StatusAdmitted:
		switch to {
		case StatusClaimed, StatusMissed, StatusLeft:
			return true
		}
	case StatusClaimed:
		switch to {
		case StatusCompleted, StatusEnded:
			return true
		}
	}
	return false
}
