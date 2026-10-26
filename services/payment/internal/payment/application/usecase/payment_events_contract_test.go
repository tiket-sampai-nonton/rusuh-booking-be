package usecase_test

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/tiket-sampai-nonton/rusuh-booking-be/pkg/contract"

	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/payment/internal/payment/domain/dto"
)

const events = "../../../../../../docs/contracts/events/"

// Platform 12.2: the events Payment publishes must follow docs/contracts/events (Booking consumes them).
func TestPaymentSucceeded_FollowsItsContract(t *testing.T) {
	require.NoError(t, contract.ValidateFile(events+"payment.succeeded.v1.schema.json", dto.PaymentSucceeded{
		BookingID: "11111111-1111-1111-1111-111111111111", AttemptID: "33333333-3333-3333-3333-333333333333",
		SettlementTime: time.Date(2026, 10, 26, 10, 30, 5, 0, time.UTC), AmountMinor: 1500000, Method: "VA_BCA",
	}))
}

func TestPaymentFailed_FollowsItsContract(t *testing.T) {
	require.NoError(t, contract.ValidateFile(events+"payment.failed.v1.schema.json", dto.PaymentFailed{
		BookingID: "11111111-1111-1111-1111-111111111111", AttemptID: "33333333-3333-3333-3333-333333333333", Reason: "DENY",
	}))
}

// The resolve API is a synchronous contract with Booking (internal-payment.yaml): outcome values are fixed.
func TestResolveOutcomes_MatchTheContract(t *testing.T) {
	require.Equal(t, "SUCCEEDED", dto.OutcomeSucceeded)
	require.Equal(t, "NOT_PAID", dto.OutcomeNotPaid)
	require.Equal(t, "NO_ATTEMPT", dto.OutcomeNoAttempt)
	require.Equal(t, "UNKNOWN", dto.OutcomeUnknown)
	require.Equal(t, "CHECK", dto.ResolveCheck)
	require.Equal(t, "CHECK_AND_CANCEL", dto.ResolveCheckAndCancel)
}
