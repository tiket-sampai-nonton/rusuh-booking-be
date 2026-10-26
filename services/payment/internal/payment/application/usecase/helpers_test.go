package usecase_test

import (
	"context"
	"testing"
	"time"

	"go.uber.org/mock/gomock"

	"github.com/tiket-sampai-nonton/rusuh-booking-be/pkg/clock"

	mock_outbound "github.com/tiket-sampai-nonton/rusuh-booking-be/services/payment/_mock/payment/port/outbound"
	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/payment/internal/payment/domain/entity"
	vo "github.com/tiket-sampai-nonton/rusuh-booking-be/services/payment/internal/payment/domain/valueobject"
	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/payment/internal/payment/port/inbound"
	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/payment/internal/payment/port/outbound"
)

const (
	bookingID = "11111111-1111-1111-1111-111111111111"
	visitorID = "22222222-2222-2222-2222-222222222222"
	attemptID = "33333333-3333-3333-3333-333333333333"
	idemKey   = "idem-key-0123456789"
)

var (
	now     = time.Date(2026, 10, 26, 10, 0, 0, 0, time.UTC)
	clk     = clock.Fixed{T: now}
	payer   = inbound.Payer{VisitorID: visitorID}
	methods = []string{"VA_BCA", "VA_BNI", "QRIS", "GOPAY"}
)

// mocks groups the outbound ports every usecase test needs.
type mocks struct {
	booking  *mock_outbound.MockIBookingClient
	attempts *mock_outbound.MockIAttemptRepository
	gateway  *mock_outbound.MockIPaymentGateway
	idem     *mock_outbound.MockIIdempotencyStore
	tokens   *mock_outbound.MockIPageTokenStore
	signer   *mock_outbound.MockISessionSigner
	verifier *mock_outbound.MockIWebhookVerifier
	cbs      *mock_outbound.MockICallbackStore
}

func newMocks(t *testing.T) mocks {
	t.Helper()
	c := gomock.NewController(t)
	return mocks{
		booking: mock_outbound.NewMockIBookingClient(c), attempts: mock_outbound.NewMockIAttemptRepository(c),
		gateway: mock_outbound.NewMockIPaymentGateway(c), idem: mock_outbound.NewMockIIdempotencyStore(c),
		tokens: mock_outbound.NewMockIPageTokenStore(c), signer: mock_outbound.NewMockISessionSigner(c),
		verifier: mock_outbound.NewMockIWebhookVerifier(c), cbs: mock_outbound.NewMockICallbackStore(c),
	}
}

func payable() outbound.Payable {
	return outbound.Payable{
		BookingID: bookingID, PublicRef: "RB-7F3A9C21", Status: "PENDING_PAYMENT", AmountMinor: 1500000,
		PaymentExpiresAt: now.Add(8 * time.Minute), VisitorID: visitorID, Version: 3,
	}
}

func lockOK() outbound.PaymentLock {
	return outbound.PaymentLock{Outcome: outbound.LockOK, Version: 4, AmountMinor: 1500000, PaymentExpiresAt: now.Add(8 * time.Minute)}
}

func attempt(status vo.AttemptStatus) entity.Attempt {
	return entity.Attempt{
		ID: attemptID, BookingID: bookingID, AttemptNo: 1, Method: vo.MethodVABCA, ProviderOrderID: "RB-7F3A9C21-1",
		Status: status, AmountMinor: 1500000, ExpiresAt: now.Add(8 * time.Minute), BookingVersion: 4,
	}
}

func ctx() context.Context { return context.Background() }
