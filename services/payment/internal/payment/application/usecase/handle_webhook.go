package usecase

import (
	"context"
	"crypto/sha256"
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

// HandleWebhook processes a Midtrans HTTP notification. A verified signature is the only proof of payment
// (invariant I-1). The work is idempotent: a duplicate or out-of-order callback changes nothing.
type HandleWebhook struct {
	verifier  outbound.IWebhookVerifier
	attempts  outbound.IAttemptRepository
	callbacks outbound.ICallbackStore
	clock     clock.IClock
}

// NewHandleWebhook wires the usecase.
func NewHandleWebhook(v outbound.IWebhookVerifier, a outbound.IAttemptRepository, c outbound.ICallbackStore, clk clock.IClock) *HandleWebhook {
	return &HandleWebhook{verifier: v, attempts: a, callbacks: c, clock: clk}
}

// HandleWebhook returns nil when the callback was understood (even if it changed nothing), so Midtrans stops
// retrying. It returns ErrSignatureInvalid (401) for a bad signature and an error for a failure worth retrying.
func (u *HandleWebhook) HandleWebhook(ctx context.Context, n dto.MidtransNotification, rawBody []byte) error {
	if n.OrderID == "" || n.TransactionStatus == "" || n.StatusCode == "" || n.GrossAmount == "" {
		return inbound.Validation("body", "order_id, transaction_status, status_code, dan gross_amount wajib diisi")
	}
	digest := sha256.Sum256(rawBody)
	valid := u.verifier.Verify(n.OrderID, n.StatusCode, n.GrossAmount, n.SignatureKey)
	cb := outbound.Callback{
		Provider: entity.Provider, Fingerprint: fingerprint(n, valid), ProviderOrderID: n.OrderID,
		NormalizedStatus: n.TransactionStatus, SignatureValid: valid, PayloadDigest: digest[:],
	}

	if !valid {
		cb.Result = outbound.CallbackRejected
		u.record(ctx, cb)
		return inbound.ErrSignatureInvalid
	}

	a, found, err := u.attempts.FindByOrderID(ctx, n.OrderID)
	if err != nil {
		return fmt.Errorf("%w: %w", inbound.ErrDependency, err)
	}
	if !found {
		slog.WarnContext(ctx, "webhook untuk order tidak dikenal", "orderId", n.OrderID)
		cb.Result = outbound.CallbackError
		return u.recordResult(ctx, cb)
	}
	amount, aerr := parseGrossAmount(n.GrossAmount)
	cb.AmountMinor = &amount
	if aerr != nil || amount != a.AmountMinor {
		// A verified callback with the wrong amount never changes state; Ops looks at the log (R4.2).
		slog.ErrorContext(ctx, "webhook nominal tidak cocok, state tidak diubah", "orderId", n.OrderID, "expected", a.AmountMinor, "got", n.GrossAmount)
		cb.Result = outbound.CallbackRejected
		return u.recordResult(ctx, cb)
	}

	status, ok := vo.MapProviderStatus(n.TransactionStatus, n.FraudStatus)
	if !ok || (status == vo.AttemptPending && a.Status == vo.AttemptCreated) {
		// No payment progress (refund, authorize, ...), or "pending" before the charge response is stored: the
		// charge flow writes the instruction, so the callback is only recorded.
		cb.Result = outbound.CallbackProcessed
		return u.recordResult(ctx, cb)
	}

	applied, err := u.apply(ctx, a, status, n)
	if err != nil {
		return fmt.Errorf("%w: %w", inbound.ErrDependency, err)
	}
	cb.Result = outbound.CallbackDuplicate
	if applied {
		cb.Result = outbound.CallbackProcessed
	}
	return u.recordResult(ctx, cb)
}

func (u *HandleWebhook) apply(ctx context.Context, a entity.Attempt, status vo.AttemptStatus, n dto.MidtransNotification) (bool, error) {
	switch status {
	case vo.AttemptSucceeded:
		var settled *time.Time
		if t, err := vo.ParseSettlementTime(n.SettlementTime); err == nil {
			settled = &t
		}
		return applySuccess(ctx, u.attempts, a, n.TransactionID, n.TransactionStatus, settled, u.clock.Now())
	case vo.AttemptFailed:
		return applyFailure(ctx, u.attempts, a, n.TransactionStatus)
	default:
		return u.attempts.Transition(ctx, outbound.TransitionSpec{AttemptID: a.ID, To: status, ProviderTxID: n.TransactionID, LastProviderStatus: n.TransactionStatus})
	}
}

func (u *HandleWebhook) recordResult(ctx context.Context, cb outbound.Callback) error {
	if _, err := u.callbacks.Record(ctx, cb); err != nil {
		return fmt.Errorf("%w: %w", inbound.ErrDependency, err) // Midtrans retries; processing is idempotent
	}
	return nil
}

// record stores a rejected callback; a failure to store must not turn a 401 into a 500.
func (u *HandleWebhook) record(ctx context.Context, cb outbound.Callback) {
	if _, err := u.callbacks.Record(ctx, cb); err != nil && !errors.Is(err, context.Canceled) {
		slog.WarnContext(ctx, "callback ditolak gagal disimpan", "error", err)
	}
}

// fingerprint identifies one callback: an exact duplicate has the same fingerprint, a status change a new one.
// It includes whether the signature was valid and the amount, so a forged callback with the same fields can never
// take the place of the genuine one in the audit trail.
func fingerprint(n dto.MidtransNotification, signatureValid bool) []byte {
	h := sha256.Sum256([]byte(fmt.Sprintf("%s|%s|%s|%s|%s|%s|%t", n.OrderID, n.TransactionStatus, n.StatusCode, n.TransactionID, n.SettlementTime, n.GrossAmount, signatureValid)))
	return h[:]
}
