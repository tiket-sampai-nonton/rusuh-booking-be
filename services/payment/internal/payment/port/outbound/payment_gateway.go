package outbound

import (
	"context"
	"errors"
	"time"

	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/payment/internal/payment/domain/entity"
	vo "github.com/tiket-sampai-nonton/rusuh-booking-be/services/payment/internal/payment/domain/valueobject"
)

//go:generate go tool mockgen -source=payment_gateway.go -destination=../../../../_mock/payment/port/outbound/payment_gateway_mock.go -package=mock_outbound

// Gateway errors tell the caller what is known about the request:
//   - ErrGatewayRejected: the provider refused it (4xx); retrying the same request will not help.
//   - ErrGatewayUnavailable: it was not sent (breaker open, bulkhead full, connection refused); safe to try again.
//   - ErrGatewayAmbiguous: it may have reached the provider (timeout, 5xx); never create a new order, check the same one.
//   - ErrOrderNotFound: the provider has no such order.
var (
	ErrGatewayRejected    = errors.New("gateway: permintaan ditolak penyedia")
	ErrGatewayUnavailable = errors.New("gateway: penyedia tidak terjangkau")
	ErrGatewayAmbiguous   = errors.New("gateway: hasil permintaan tidak pasti")
	ErrOrderNotFound      = errors.New("gateway: order tidak ditemukan")
)

// ChargeRequest asks the provider for a payment instruction. Expiry is the time left until the booking's
// payment deadline, so the provider transaction never outlives the booking (Property 5).
type ChargeRequest struct {
	OrderID     string
	Method      vo.Method
	AmountMinor int64
	OrderTime   time.Time
	Expiry      time.Duration
}

// ChargeResult is what the provider returned for a charge.
type ChargeResult struct {
	ProviderTxID string
	Instruction  entity.Instruction
}

// GatewayStatus is the provider's view of one order, already normalised to an attempt status.
type GatewayStatus struct {
	Status         vo.AttemptStatus
	ProviderTxID   string
	SettlementTime *time.Time
	RawStatus      string
}

// IPaymentGateway is the payment provider (Midtrans). Every call goes through a breaker and bulkhead.
type IPaymentGateway interface {
	Charge(ctx context.Context, req ChargeRequest) (ChargeResult, error)
	Status(ctx context.Context, orderID string) (GatewayStatus, error)
	Cancel(ctx context.Context, orderID string) error
}

// IWebhookVerifier checks the signature_key of a Midtrans HTTP notification. The server key stays inside the adapter.
type IWebhookVerifier interface {
	Verify(orderID, statusCode, grossAmount, signatureKey string) bool
}
