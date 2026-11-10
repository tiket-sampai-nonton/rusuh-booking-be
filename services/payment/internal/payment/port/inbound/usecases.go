package inbound

import (
	"context"

	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/payment/internal/payment/domain/dto"
)

//go:generate go tool mockgen -source=usecases.go -destination=../../../../_mock/payment/port/inbound/usecases_mock.go -package=mock_inbound

// Payer is the verified caller of the payment page. VisitorID comes from the visitor token. BookingID is set
// only when the caller came through an rb_pay cookie, which is valid for that one booking.
type Payer struct {
	VisitorID string
	BookingID string
}

// IGetPaymentPage serves GET /api/v1/payments/{bookingId}/page.
type IGetPaymentPage interface {
	GetPage(ctx context.Context, payer Payer, bookingID string) (dto.PaymentPage, error)
}

// IGetPaymentStatus serves GET /api/v1/payments/{bookingId}/status (read-only).
type IGetPaymentStatus interface {
	GetStatus(ctx context.Context, payer Payer, bookingID string) (dto.PaymentStatus, error)
}

// ICreateAttempt serves POST /api/v1/payments/{bookingId}/attempts.
type ICreateAttempt interface {
	CreateAttempt(ctx context.Context, payer Payer, bookingID, idempotencyKey string, req dto.CreateAttemptRequest) (dto.Attempt, error)
}

// ICreatePageToken serves POST /api/v1/payments/{bookingId}/page-tokens.
type ICreatePageToken interface {
	CreatePageToken(ctx context.Context, payer Payer, bookingID string) (dto.PageToken, error)
}

// IExchangePageToken serves POST /api/v1/payment-page-sessions.
type IExchangePageToken interface {
	ExchangePageToken(ctx context.Context, pageToken string) (dto.PageSession, error)
}

// IHandleWebhook serves POST /api/v1/webhooks/midtrans. rawBody is the exact request body (for the payload digest).
type IHandleWebhook interface {
	HandleWebhook(ctx context.Context, n dto.MidtransNotification, rawBody []byte) error
}

// IResolvePayment serves POST /internal/v1/payments/{bookingId}/resolve.
type IResolvePayment interface {
	ResolvePayment(ctx context.Context, bookingID, mode string) (dto.ResolveResponse, error)
}
