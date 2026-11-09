package simulator_test

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tiket-sampai-nonton/rusuh-booking-be/pkg/clock"

	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/payment/internal/payment/infrastructure/simulator"
	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/payment/internal/payment/port/outbound"
)

type stubGateway struct {
	mu                    sync.Mutex
	charges, status, canc int
}

func (s *stubGateway) Charge(context.Context, outbound.ChargeRequest) (outbound.ChargeResult, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.charges++
	return outbound.ChargeResult{ProviderTxID: "tx"}, nil
}
func (s *stubGateway) Status(context.Context, string) (outbound.GatewayStatus, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.status++
	return outbound.GatewayStatus{RawStatus: "pending"}, nil
}
func (s *stubGateway) Cancel(context.Context, string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.canc++
	return nil
}

type mutableClock struct {
	mu sync.Mutex
	t  time.Time
}

func (m *mutableClock) Now() time.Time  { m.mu.Lock(); defer m.mu.Unlock(); return m.t }
func (m *mutableClock) set(t time.Time) { m.mu.Lock(); m.t = t; m.mu.Unlock() }

var (
	_ outbound.IPaymentGateway = (*simulator.FaultGateway)(nil)
	_ outbound.IGatewayFaults  = (*simulator.FaultGateway)(nil)
	_ clock.IClock             = (*mutableClock)(nil)
)

func TestFaultGateway_TimeoutUntilTTL(t *testing.T) {
	ctx := context.Background()
	start := time.Date(2026, 1, 1, 10, 0, 0, 0, time.UTC)
	clk := &mutableClock{t: start}
	inner := &stubGateway{}
	g := simulator.NewFaultGateway(inner, clk)

	g.ArmTimeout("RB-1", 2*time.Minute)

	_, err := g.Status(ctx, "RB-1")
	assert.ErrorIs(t, err, outbound.ErrGatewayAmbiguous)
	assert.ErrorIs(t, g.Cancel(ctx, "RB-1"), outbound.ErrGatewayAmbiguous)
	assert.Zero(t, inner.status+inner.canc, "armed order never reaches the provider")

	_, err = g.Status(ctx, "RB-OTHER")
	assert.NoError(t, err, "other orders are untouched")

	_, err = g.Charge(ctx, outbound.ChargeRequest{OrderID: "RB-1"})
	assert.NoError(t, err, "Charge always delegates")

	clk.set(start.Add(2*time.Minute + time.Second))
	_, err = g.Status(ctx, "RB-1")
	assert.NoError(t, err, "after the TTL the provider is reachable again")
	assert.NoError(t, g.Cancel(ctx, "RB-1"))
	assert.Equal(t, 1, inner.charges)
}

func TestFaultGateway_ClearRemovesFault(t *testing.T) {
	g := simulator.NewFaultGateway(&stubGateway{}, &mutableClock{t: time.Now()})
	g.ArmTimeout("RB-1", time.Hour)
	g.Clear("RB-1")
	_, err := g.Status(context.Background(), "RB-1")
	assert.NoError(t, err)
}

func TestFaultGateway_ConcurrentArmAndRead(t *testing.T) {
	g := simulator.NewFaultGateway(&stubGateway{}, clock.System{})
	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(2)
		go func() { defer wg.Done(); g.ArmTimeout("RB-1", time.Minute) }()
		go func() { defer wg.Done(); _, _ = g.Status(context.Background(), "RB-1") }()
	}
	wg.Wait()
	_, err := g.Status(context.Background(), "RB-1")
	require.ErrorIs(t, err, outbound.ErrGatewayAmbiguous)
}
