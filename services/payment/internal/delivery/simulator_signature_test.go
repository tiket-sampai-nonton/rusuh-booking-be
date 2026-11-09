package delivery

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	"github.com/tiket-sampai-nonton/rusuh-booking-be/pkg/apperror"
	"github.com/tiket-sampai-nonton/rusuh-booking-be/pkg/clock"

	mock_outbound "github.com/tiket-sampai-nonton/rusuh-booking-be/services/payment/_mock/payment/port/outbound"
	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/payment/internal/payment/application/usecase"
	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/payment/internal/payment/domain/dto"
	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/payment/internal/payment/domain/entity"
	vo "github.com/tiket-sampai-nonton/rusuh-booking-be/services/payment/internal/payment/domain/valueobject"
	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/payment/internal/payment/infrastructure/midtrans"
	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/payment/internal/payment/port/inbound"
	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/payment/internal/payment/port/outbound"
)

// P-12 (invariant 5, Req 2.14), wired the way the composition root wires it: the real HandleWebhook with the
// real SignatureVerifier on key K1, and a simulator whose signer uses key K2. The verifier rejects it, the
// simulator reports an internal error (never the webhook's 401, never swallowed), and no state changes.
func TestSimulator_SignerKeyMismatchIsInternalErrorAndChangesNothing(t *testing.T) {
	const (
		attemptID = "33333333-3333-3333-3333-333333333333"
		bookingID = "11111111-1111-1111-1111-111111111111"
		visitorID = "22222222-2222-2222-2222-222222222222"
	)
	now := time.Date(2026, 10, 26, 10, 0, 0, 0, time.UTC)
	clk := clock.Fixed{T: now}
	ctrl := gomock.NewController(t)
	attempts := mock_outbound.NewMockIAttemptRepository(ctrl) // Transition is not expected: a call fails the test
	bookings := mock_outbound.NewMockIBookingClient(ctrl)
	callbacks := mock_outbound.NewMockICallbackStore(ctrl)
	faults := mock_outbound.NewMockIGatewayFaults(ctrl)

	a := entity.Attempt{ID: attemptID, BookingID: bookingID, ProviderOrderID: "RB-7F3A9C21-1", Status: vo.AttemptPending,
		AmountMinor: 1500000, ExpiresAt: now.Add(8 * time.Minute)}
	attempts.EXPECT().FindByID(gomock.Any(), attemptID).Return(a, true, nil)
	bookings.EXPECT().GetPayable(gomock.Any(), bookingID).Return(outbound.Payable{BookingID: bookingID, VisitorID: visitorID, Status: "PENDING_PAYMENT"}, nil)
	faults.EXPECT().Clear(a.ProviderOrderID)
	var recorded []outbound.Callback
	callbacks.EXPECT().Record(gomock.Any(), gomock.Any()).DoAndReturn(func(_ context.Context, cb outbound.Callback) (bool, error) {
		recorded = append(recorded, cb)
		return true, nil
	})

	hook := usecase.NewHandleWebhook(midtrans.NewSignatureVerifier("kunci-K1"), attempts, callbacks, clk)
	sim := usecase.NewSimulatePayment(attempts, bookings, hook, midtrans.NewWebhookSigner("kunci-K2"), faults, clk)

	_, err := sim.Simulate(context.Background(), inbound.Payer{VisitorID: visitorID}, attemptID, "SETTLE")
	require.Error(t, err)
	var ae *apperror.Error
	assert.False(t, errors.As(err, &ae), "must not be the webhook's 401 apperror: a key mismatch is a server misconfiguration (500)")
	assert.NotContains(t, err.Error(), "kunci-K1")
	assert.NotContains(t, err.Error(), "kunci-K2")
	require.Len(t, recorded, 1)
	assert.Equal(t, outbound.CallbackRejected, recorded[0].Result)
	assert.False(t, recorded[0].SignatureValid)

	// A direct webhook with a bad signature is still a plain 401.
	direct := usecase.NewHandleWebhook(midtrans.NewSignatureVerifier("kunci-K1"), attempts, callbacks, clk)
	callbacks.EXPECT().Record(gomock.Any(), gomock.Any()).Return(true, nil)
	err = direct.HandleWebhook(context.Background(), dto.MidtransNotification{
		OrderID: "x", TransactionStatus: "settlement", StatusCode: "200", GrossAmount: "1.00", SignatureKey: "salah"}, []byte("{}"))
	assert.ErrorIs(t, err, inbound.ErrSignatureInvalid)
}
