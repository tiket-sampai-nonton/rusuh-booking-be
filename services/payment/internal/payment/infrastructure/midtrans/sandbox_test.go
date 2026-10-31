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

// A Snap token creates no Core API order until the payer picks a channel on the Snap page, so before payment the
// status of a fresh order is "not found" (the usecase treats that as NOT_PAID). Paying needs the Snap page and is manual.
func TestSandbox_SnapTokenThenUnpaidStatus(t *testing.T) {
	g := sandboxGateway(t)
	ctx := context.Background()
	id := orderID()

	res, err := g.Charge(ctx, outbound.ChargeRequest{OrderID: id, Method: vo.MethodVABCA, AmountMinor: 150000, OrderTime: time.Now(), Expiry: 5 * time.Minute})
	require.NoError(t, err)
	assert.Equal(t, "SNAP", res.Instruction.Type)
	assert.NotEmpty(t, res.Instruction.SnapToken)
	assert.Contains(t, res.Instruction.RedirectURL, res.Instruction.SnapToken)

	_, err = g.Status(ctx, id)
	assert.ErrorIs(t, err, outbound.ErrOrderNotFound)
	_, err = g.Status(ctx, orderID())
	assert.ErrorIs(t, err, outbound.ErrOrderNotFound)
}
