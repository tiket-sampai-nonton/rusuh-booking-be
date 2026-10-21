// Package ids generates identifiers for the Booking service: UUIDs for aggregate ids and short public
// references for bookings and tickets.
package ids

import (
	"crypto/rand"
	"fmt"

	pkgrabbitmq "github.com/tiket-sampai-nonton/rusuh-booking-be/pkg/rabbitmq"
)

// Gen generates identifiers. It satisfies the usecase IDGen port.
type Gen struct{}

// New returns a generator.
func New() *Gen { return &Gen{} }

// NewID returns a random UUIDv4.
func (g *Gen) NewID() string { return pkgrabbitmq.NewID() }

// NewPublicRef returns a short, human-readable reference such as RB-7F3A9C21. It is random (not a
// running counter) to avoid leaking volume; uniqueness is still guaranteed by the DB UNIQUE column.
func (g *Gen) NewPublicRef() string {
	var b [4]byte
	_, _ = rand.Read(b[:])
	return fmt.Sprintf("RB-%02X%02X%02X%02X", b[0], b[1], b[2], b[3])
}
