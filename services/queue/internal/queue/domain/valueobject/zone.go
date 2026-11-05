package valueobject

// Zone classifies an entry's likelihood of being served given current stock (design §2.3, §4.5).
type Zone string

// Zones.
const (
	ZoneGuaranteed Zone = "GUARANTEED"
	ZonePossible   Zone = "POSSIBLE"
	ZoneNotEnough  Zone = "NOT_ENOUGH"
)
