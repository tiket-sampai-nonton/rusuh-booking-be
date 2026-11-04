package valueobject

// ShouldRelease reports whether the allocation granted for an entry must be released at Booking, given the status
// that promote.lua reported for that entry atomically with its decision (design §5.4). Only provably terminal entries
// qualify: releasing a RESERVED row turns it into RELEASED, after which Booking answers ALREADY_ACTIVE for the same
// entry forever, so a release for an entry that is merely deferred would end a live buyer.
func ShouldRelease(skipStatus string) bool {
	switch skipStatus {
	case "NO_ENTRY", "LEFT", "ENDED", "COMPLETED", "MISSED", "ADMITTED_OTHER":
		return true
	}
	return false
}
