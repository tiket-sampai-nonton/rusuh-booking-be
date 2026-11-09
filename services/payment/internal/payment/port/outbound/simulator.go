package outbound

import "time"

//go:generate go tool mockgen -source=simulator.go -destination=../../../../_mock/payment/port/outbound/simulator_mock.go -package=mock_outbound

// IWebhookSigner signs a notification exactly as Midtrans would. The server key stays inside the adapter.
type IWebhookSigner interface {
	Sign(orderID, statusCode, grossAmount string) string
}

// IGatewayFaults arms a timeout for one provider order: until ttl passes, Status and Cancel for that order
// are ambiguous (simulator scenario TIMEOUT).
type IGatewayFaults interface {
	ArmTimeout(orderID string, ttl time.Duration)
	// Clear removes an armed fault (any later non-TIMEOUT scenario).
	Clear(orderID string)
}
