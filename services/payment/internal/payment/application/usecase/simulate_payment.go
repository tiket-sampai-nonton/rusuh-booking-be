package usecase

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/tiket-sampai-nonton/rusuh-booking-be/pkg/clock"

	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/payment/internal/payment/domain/dto"
	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/payment/internal/payment/domain/entity"
	vo "github.com/tiket-sampai-nonton/rusuh-booking-be/services/payment/internal/payment/domain/valueobject"
	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/payment/internal/payment/port/inbound"
	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/payment/internal/payment/port/outbound"
)

// simulatorTimeoutTTL is how long a TIMEOUT scenario keeps Status/Cancel ambiguous for the order.
const simulatorTimeoutTTL = 2 * time.Minute

const settlementLayout = "2006-01-02 15:04:05"

// SimulatePayment drives a payment attempt through a scenario for local/ci environments. It never changes
// state itself: it signs Midtrans-shaped notifications with the server key and hands them to the real webhook
// usecase, so idempotency, amount check, transition and outbox are the production code path (invariant 5).
type SimulatePayment struct {
	attempts outbound.IAttemptRepository
	booking  outbound.IBookingClient
	webhook  inbound.IHandleWebhook
	signer   outbound.IWebhookSigner
	faults   outbound.IGatewayFaults
	clock    clock.IClock
}

// NewSimulatePayment wires the usecase.
func NewSimulatePayment(a outbound.IAttemptRepository, b outbound.IBookingClient, w inbound.IHandleWebhook,
	s outbound.IWebhookSigner, f outbound.IGatewayFaults, clk clock.IClock) *SimulatePayment {
	return &SimulatePayment{attempts: a, booking: b, webhook: w, signer: s, faults: f, clock: clk}
}

// Simulate runs scenario against the caller's own attempt.
func (u *SimulatePayment) Simulate(ctx context.Context, payer inbound.Payer, attemptID, scenario string) (dto.SimulatorResult, error) {
	sc, ok := vo.ParseScenario(scenario)
	if !ok {
		return dto.SimulatorResult{}, inbound.Validation("scenario", "scenario tidak dikenal")
	}
	if !isUUID(attemptID) {
		return dto.SimulatorResult{}, inbound.ErrNotFound
	}
	a, found, err := u.attempts.FindByID(ctx, attemptID)
	if err != nil {
		return dto.SimulatorResult{}, fmt.Errorf("%w: %w", inbound.ErrDependency, err)
	}
	if !found {
		return dto.SimulatorResult{}, inbound.ErrNotFound
	}
	if _, err := loadOwnedPayable(ctx, u.booking, payer, a.BookingID); err != nil {
		return dto.SimulatorResult{}, err
	}

	if sc == vo.ScenarioTimeout {
		u.faults.ArmTimeout(a.ProviderOrderID, simulatorTimeoutTTL)
	} else {
		u.faults.Clear(a.ProviderOrderID)
		for _, n := range u.notifications(sc, a) {
			raw, merr := json.Marshal(n)
			if merr != nil {
				return dto.SimulatorResult{}, fmt.Errorf("simulator: marshal notifikasi: %w", merr)
			}
			if err := u.webhook.HandleWebhook(ctx, n, raw); err != nil {
				return dto.SimulatorResult{}, u.webhookError(ctx, err)
			}
		}
	}
	slog.InfoContext(ctx, "payment simulator", "simulated", true, "scenario", string(sc), "attemptId", a.ID)
	return dto.SimulatorResult{Simulated: true, Scenario: string(sc)}, nil
}

// webhookError keeps dependency errors as they are. A rejected signature means the signer and the verifier
// disagree on the server key: a server misconfiguration, so it must not come back as the webhook's 401.
func (u *SimulatePayment) webhookError(ctx context.Context, err error) error {
	if errors.Is(err, inbound.ErrSignatureInvalid) {
		slog.ErrorContext(ctx, "payment simulator: signature ditolak verifier; kunci signer dan verifier berbeda")
		return errors.New("simulator: signature ditolak oleh verifier webhook (konfigurasi server key tidak konsisten)")
	}
	return err
}

// notifications builds the signed notification sequence of one scenario.
func (u *SimulatePayment) notifications(sc vo.Scenario, a entity.Attempt) []dto.MidtransNotification {
	now := u.clock.Now().In(vo.WIB()).Format(settlementLayout)
	late := a.ExpiresAt.Add(-time.Second).In(vo.WIB()).Format(settlementLayout)
	settle := func(at string) dto.MidtransNotification { return u.notification(a, "200", "settlement", at) }
	switch sc {
	case vo.ScenarioSettle:
		return []dto.MidtransNotification{settle(now)}
	case vo.ScenarioSettleLate:
		return []dto.MidtransNotification{settle(late)}
	case vo.ScenarioExpire:
		return []dto.MidtransNotification{u.notification(a, "407", "expire", "")}
	case vo.ScenarioDeny:
		return []dto.MidtransNotification{u.notification(a, "202", "deny", "")}
	case vo.ScenarioDuplicateWebhook:
		return []dto.MidtransNotification{settle(now), settle(now)}
	case vo.ScenarioOutOfOrder:
		return []dto.MidtransNotification{settle(now), u.notification(a, "201", "pending", "")}
	}
	return nil
}

func (u *SimulatePayment) notification(a entity.Attempt, code, status, settledAt string) dto.MidtransNotification {
	gross := fmt.Sprintf("%d.00", a.AmountMinor)
	return dto.MidtransNotification{
		OrderID: a.ProviderOrderID, StatusCode: code, TransactionStatus: status, GrossAmount: gross,
		TransactionID: "SIM-" + a.ID, SettlementTime: settledAt,
		SignatureKey: u.signer.Sign(a.ProviderOrderID, code, gross),
	}
}
