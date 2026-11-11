package usecase

import (
	"bytes"
	"encoding/json"

	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/booking/internal/booking/port/inbound"
)

// maxDescriptionBytes caps a stored description document.
const maxDescriptionBytes = 200 << 10

// validateDescription accepts an absent value, JSON null (clear), a structured JSON object (longDesc, lineup,
// location, gallery, terms), or a legacy Slate document: a JSON array whose elements are all objects.
// The contents are not interpreted; the client owns the schema.
func validateDescription(raw json.RawMessage) error {
	if len(raw) == 0 || string(raw) == "null" {
		return nil
	}
	if len(raw) > maxDescriptionBytes {
		return inbound.Validation("description", "ukuran description maksimal 200 KB")
	}
	if bytes.HasPrefix(bytes.TrimSpace(raw), []byte("{")) {
		if !json.Valid(raw) {
			return inbound.Validation("description", "description harus JSON yang valid")
		}
		return nil
	}
	var nodes []json.RawMessage
	if err := json.Unmarshal(raw, &nodes); err != nil {
		return inbound.Validation("description", "description harus objek JSON atau array node Slate")
	}
	for _, n := range nodes {
		if !bytes.HasPrefix(bytes.TrimSpace(n), []byte("{")) {
			return inbound.Validation("description", "setiap node description harus objek")
		}
	}
	return nil
}
