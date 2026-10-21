package dto

// ClaimResponse is the 200 data of POST /api/v1/queue-entries/{entryId}/claim (design §2.4).
type ClaimResponse struct {
	AdmissionToken string `json:"admissionToken"`
	BookingURL     string `json:"bookingUrl"`
}
