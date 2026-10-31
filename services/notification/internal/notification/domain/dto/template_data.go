package dto

import "time"

// EticketData is the render data for the eticket template.
type EticketData struct {
	PublicRef     string
	EventName     string
	EventStartsAt string
	EventVenue    string
	Tickets       []EticketTicket
	// TicketURL opens the tickets without logging in (ticket-link token inside); empty when no link base is configured.
	TicketURL string
}

// EticketTicket is one ticket as the e-ticket template shows it. QRCID names the inline image (empty when the QR could
// not be made; the ticket code is still shown), QRPayload is the text the QR encodes (same as Booking's qrPayload).
type EticketTicket struct {
	TicketInfo
	QRCID     string
	QRPayload string
}

// ExpiredData is the render data for the expired template.
type ExpiredData struct {
	PublicRef string
	Reason    string
}

// ReviewData is the render data for the review template.
type ReviewData struct {
	BookingID   string
	AmountMinor int64
	AttemptHint string
}

// OTPData is the render data for the otp_login template.
type OTPData struct {
	Code      string
	ExpiresAt time.Time
	Locale    string
}
