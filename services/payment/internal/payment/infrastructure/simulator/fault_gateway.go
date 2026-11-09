// Package simulator holds the local-only adapters of the payment simulator (APP_ENV local|ci).
package simulator

import (
	"context"
	"sync"
	"time"

	"github.com/tiket-sampai-nonton/rusuh-booking-be/pkg/clock"

	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/payment/internal/payment/port/outbound"
)

// FaultGateway wraps the real gateway. While a timeout is armed for an order, Status and Cancel for it return
// ErrGatewayAmbiguous (as a provider timeout would) so Booking's resolve ends UNKNOWN. Charge and unarmed
// orders always delegate.
type FaultGateway struct {
	inner outbound.IPaymentGateway
	clock clock.IClock

	mu       sync.Mutex
	timeouts map[string]time.Time // order id → armed until
}

// NewFaultGateway wraps inner.
func NewFaultGateway(inner outbound.IPaymentGateway, clk clock.IClock) *FaultGateway {
	return &FaultGateway{inner: inner, clock: clk, timeouts: map[string]time.Time{}}
}

// ArmTimeout makes Status/Cancel of orderID ambiguous for ttl.
func (g *FaultGateway) ArmTimeout(orderID string, ttl time.Duration) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.timeouts[orderID] = g.clock.Now().Add(ttl)
}

// Clear removes an armed fault.
func (g *FaultGateway) Clear(orderID string) {
	g.mu.Lock()
	defer g.mu.Unlock()
	delete(g.timeouts, orderID)
}

func (g *FaultGateway) armed(orderID string) bool {
	g.mu.Lock()
	defer g.mu.Unlock()
	until, ok := g.timeouts[orderID]
	if !ok {
		return false
	}
	if !g.clock.Now().Before(until) {
		delete(g.timeouts, orderID)
		return false
	}
	return true
}

// Charge always delegates.
func (g *FaultGateway) Charge(ctx context.Context, req outbound.ChargeRequest) (outbound.ChargeResult, error) {
	return g.inner.Charge(ctx, req)
}

// Status is ambiguous while a timeout is armed.
func (g *FaultGateway) Status(ctx context.Context, orderID string) (outbound.GatewayStatus, error) {
	if g.armed(orderID) {
		return outbound.GatewayStatus{}, outbound.ErrGatewayAmbiguous
	}
	return g.inner.Status(ctx, orderID)
}

// Cancel is ambiguous while a timeout is armed.
func (g *FaultGateway) Cancel(ctx context.Context, orderID string) error {
	if g.armed(orderID) {
		return outbound.ErrGatewayAmbiguous
	}
	return g.inner.Cancel(ctx, orderID)
}
