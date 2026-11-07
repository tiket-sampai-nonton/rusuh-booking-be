package usecase

import (
	"bytes"
	"encoding/json"

	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/booking/internal/booking/port/inbound"
)

// maxDescriptionBytes caps a stored Slate document.
const maxDescriptionBytes = 200 << 10

// validateDescription accepts an absent value, JSON null (clear), or a Slate document: a JSON array whose
// elements are all objects. The node contents are not interpreted; the client owns the Slate schema.
func validateDescription(raw json.RawMessage) error {
	if len(raw) == 0 || string(raw) == "null" {
		return nil
	}
	if len(raw) > maxDescriptionBytes {
		return inbound.Validation("description", "ukuran description maksimal 200 KB")
	}
	var nodes []json.RawMessage
	if err := json.Unmarshal(raw, &nodes); err != nil {
		return inbound.Validation("description", "description harus array node Slate (JSON)")
	}
	for _, n := range nodes {
		if !bytes.HasPrefix(bytes.TrimSpace(n), []byte("{")) {
			return inbound.Validation("description", "setiap node description harus objek")
		}
	}
	return nil
}
