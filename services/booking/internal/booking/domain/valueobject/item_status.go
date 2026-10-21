package valueobject

// ItemStatus is the lifecycle state of a booking item (matches booking_items.item_status CHECK).
type ItemStatus string

// Item statuses (migration 0001_init.up.sql booking_items.item_status CHECK; design §6.3).
const (
	ItemHeld     ItemStatus = "HELD"
	ItemPaid     ItemStatus = "PAID"
	ItemReleased ItemStatus = "RELEASED"
	ItemReplaced ItemStatus = "REPLACED"
)

// Valid reports whether s is a known item status.
func (s ItemStatus) Valid() bool {
	switch s {
	case ItemHeld, ItemPaid, ItemReleased, ItemReplaced:
		return true
	default:
		return false
	}
}

// IsActive reports whether the item still occupies stock (HELD or PAID) — the states guarded by the
// uq_attendee_nik_per_event and uq_booking_item_nik_active unique indexes.
func (s ItemStatus) IsActive() bool {
	return s == ItemHeld || s == ItemPaid
}
