//go:build sandbox

package midtrans_test

import (
	"context"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tiket-sampai-nonton/rusuh-booking-be/pkg/dotenv"
	"github.com/tiket-sampai-nonton/rusuh-booking-be/pkg/rabbitmq"

	vo "github.com/tiket-sampai-nonton/rusuh-booking-be/services/payment/internal/payment/domain/valueobject"
	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/payment/internal/payment/infrastructure/midtrans"
	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/payment/internal/payment/port/outbound"
)

// Opt-in contract test against the real Midtrans Sandbox: go test -tags sandbox -run TestSandbox -timeout 5m ./internal/payment/infrastructure/midtrans/
// MIDTRANS_SERVER_KEY comes from the environment or services/payment/.env (never from the command line). It creates
// real Sandbox orders (harmless, no money) and takes about a minute because it waits for a real expiry.
func sandboxGateway(t *testing.T) *midtrans.Gateway {
	t.Helper()
	_ = dotenv.Load("../../../../.env") // services/payment/.env; variables already in the environment win
	key := os.Getenv("MIDTRANS_SERVER_KEY")
	if key == "" {
		t.Skip("MIDTRANS_SERVER_KEY tidak diset")
	}
	l := midtrans.Limits{Timeout: 10 * time.Second, FailureThreshold: 5, Cooldown: 20 * time.Second, Bulkhead: 5}
	g, err := midtrans.New(midtrans.Config{BaseURL: "https://api.sandbox.midtrans.com", ServerKey: key, Charge: l, Status: l}, time.Now)
	require.NoError(t, err)
	return g
}

func orderID() string { return fmt.Sprintf("RB-SBX%s-1", rabbitmq.NewID()[:8]) }

func charge(t *testing.T, g *midtrans.Gateway, id string, expiry time.Duration) {
	t.Helper()
	res, err := g.Charge(context.Background(), outbound.ChargeRequest{OrderID: id, Method: vo.MethodVABCA, AmountMinor: 150000, OrderTime: time.Now(), Expiry: expiry})
	require.NoError(t, err)
	assert.Equal(t, "VA", res.Instruction.Type)
	assert.NotEmpty(t, res.Instruction.VANumber)
	assert.NotEmpty(t, res.ProviderTxID)
}

func TestSandbox_ChargeStatusCancel(t *testing.T) {
	g := sandboxGateway(t)
	ctx := context.Background()
	id := orderID()
	charge(t, g, id, 5*time.Minute)

	st, err := g.Status(ctx, id)
	require.NoError(t, err)
	assert.Equal(t, vo.AttemptPending, st.Status)

	require.NoError(t, g.Cancel(ctx, id))
	st, err = g.Status(ctx, id)
	require.NoError(t, err)
	assert.Equal(t, vo.AttemptCancelled, st.Status)

	// Midtrans answers HTTP 200 with status_code 412 when the order can no longer change.
	assert.ErrorIs(t, g.Cancel(ctx, id), outbound.ErrGatewayRejected)

	_, err = g.Status(ctx, orderID())
	assert.ErrorIs(t, err, outbound.ErrOrderNotFound, "Midtrans menjawab HTTP 200 dengan status_code 404")
	assert.ErrorIs(t, g.Cancel(ctx, orderID()), outbound.ErrOrderNotFound)
}

// A real expiry: the smallest custom_expiry accepted everywhere is 30 s (20 s is rejected for VA BCA); Midtrans answers the status with status_code 407 and transaction_status expire.
func TestSandbox_RealExpiry(t *testing.T) {
	g := sandboxGateway(t)
	ctx := context.Background()
	id := orderID()
	charge(t, g, id, 30*time.Second)

	deadline := time.Now().Add(120 * time.Second)
	var last vo.AttemptStatus
	for time.Now().Before(deadline) {
		st, err := g.Status(ctx, id)
		require.NoError(t, err, "status 407 + transaction_status expire bukan penolakan")
		if last = st.Status; last == vo.AttemptExpired {
			break
		}
		time.Sleep(5 * time.Second)
	}
	assert.Equal(t, vo.AttemptExpired, last, "order tidak kedaluwarsa dalam 120 detik")

	// An expired order cannot be cancelled any more.
	assert.ErrorIs(t, g.Cancel(ctx, id), outbound.ErrGatewayRejected)
}
