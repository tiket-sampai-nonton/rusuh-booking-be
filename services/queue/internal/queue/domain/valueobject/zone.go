package valueobject

// Zone classifies an entry's likelihood of being served given current stock (design §2.3, §4.5).
type Zone string

// Zones.
const (
	ZoneGuaranteed Zone = "GUARANTEED"
	ZonePossible   Zone = "POSSIBLE"
	ZoneNotEnough  Zone = "NOT_ENOUGH"
)

// ZoneFor returns the zone for seq relative to the guaranteed/possible boundaries.
// seq <= guaranteedUntil → GUARANTEED; seq <= possibleUntil → POSSIBLE; else NOT_ENOUGH.
func ZoneFor(seq, guaranteedUntil, possibleUntil Seq) Zone {
	switch {
	case seq <= guaranteedUntil:
		return ZoneGuaranteed
	case seq <= possibleUntil:
		return ZonePossible
	default:
		return ZoneNotEnough
	}
}
