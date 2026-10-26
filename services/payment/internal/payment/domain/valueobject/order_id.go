package valueobject

import (
	"errors"
	"fmt"
	"regexp"
)

// maxOrderIDLength is the Midtrans limit for order_id.
const maxOrderIDLength = 50

var orderIDChars = regexp.MustCompile(`^[A-Za-z0-9_.~-]+$`)

// ErrOrderID means the booking reference cannot form a valid Midtrans order_id.
var ErrOrderID = errors.New("order id tidak valid")

// NewOrderID builds the deterministic provider order id "{publicRef}-{attemptNo}", e.g. RB-7F3A9C21-1.
// publicRef already carries the "RB-" prefix (Booking), so design Δ3 "RB-{publicRef}-{n}" would repeat it.
func NewOrderID(publicRef string, attemptNo int) (string, error) {
	id := fmt.Sprintf("%s-%d", publicRef, attemptNo)
	if publicRef == "" || attemptNo < 1 || len(id) > maxOrderIDLength || !orderIDChars.MatchString(id) {
		return "", fmt.Errorf("%w: %q", ErrOrderID, id)
	}
	return id, nil
}
