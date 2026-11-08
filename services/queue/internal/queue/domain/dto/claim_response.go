package dto

// ClaimResponse is the 200 data of POST /api/v1/queue-entries/{entryId}/claim (design §2.4).
type ClaimResponse struct {
	AdmissionToken string `json:"admissionToken"`
	// BookingURL is the public booking page, {BOOKING_PAGE_BASE_URL}/{eventId}. It contains no token; the client
	// sends AdmissionToken to POST /api/v1/booking-sessions.
	BookingURL string `json:"bookingUrl"`
}
