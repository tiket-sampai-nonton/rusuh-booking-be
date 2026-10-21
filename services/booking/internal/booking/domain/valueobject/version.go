package valueobject

// Version is the monotonic optimistic-concurrency counter of an aggregate (bookings.version and the
// event store stream version). It starts at 1 after the first event.
type Version int

// Next returns the version that follows v.
func (v Version) Next() Version { return v + 1 }

// Matches reports whether v equals other (used for optimistic concurrency checks on edit).
func (v Version) Matches(other Version) bool { return v == other }
