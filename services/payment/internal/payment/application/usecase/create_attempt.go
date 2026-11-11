package usecase

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/tiket-sampai-nonton/rusuh-booking-be/pkg/clock"

	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/payment/internal/payment/domain/dto"
	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/payment/internal/payment/domain/entity"
	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/payment/internal/payment/domain/mapper"
	vo "github.com/tiket-sampai-nonton/rusuh-booking-be/services/payment/internal/payment/domain/valueobject"
	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/payment/internal/payment/port/inbound"
	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/payment/internal/payment/port/outbound"
)

const (
	scopeCreateAttempt = "create_attempt"
	idempotencyTTL     = 24 * time.Hour
	// minChargeExpiry is the least time left before the booking deadline that still allows a charge: Snap expiry is whole minutes.
	minChargeExpiry = time.Minute
)

// CreateAttempt starts a payment for a booking (design §9.1): idempotency → payment-lock → close the previous
// attempt → store CREATED → charge Midtrans (outside any DB transaction) → PENDING or UNKNOWN.
type CreateAttempt struct {
	booking  outbound.IBookingClient
	attempts outbound.IAttemptRepository
	idem     outbound.IIdempotencyStore
	gateway  outbound.IPaymentGateway
	clock    clock.IClock
	enabled  map[vo.Method]bool
	returns  ReturnURLs
	// maxAttempts caps attempts per booking (PAYMENT_MAX_ATTEMPTS); 0 = no cap. Every attempt counts, including the
	// cancelled ones: each created a transaction at Midtrans.
	maxAttempts int
}

// ReturnURLs are the FE pages Snap sends the payer back to; both verify the status with the backend before showing a result.
// They may contain {bookingId} and {attemptId}, filled per attempt by fill (both are UUIDs, so URL-safe).
type ReturnURLs struct{ Success, Failed string }

func (r ReturnURLs) fill(a entity.Attempt) ReturnURLs {
	f := strings.NewReplacer("{bookingId}", a.BookingID, "{attemptId}", a.ID).Replace
	return ReturnURLs{Success: f(r.Success), Failed: f(r.Failed)}
}

// NewCreateAttempt wires the usecase; methods are the enabled payment methods.
func NewCreateAttempt(b outbound.IBookingClient, a outbound.IAttemptRepository, idem outbound.IIdempotencyStore,
	g outbound.IPaymentGateway, clk clock.IClock, methods []string, returns ReturnURLs) *CreateAttempt {
	enabled := map[vo.Method]bool{}
	for _, m := range methods {
		if method, ok := vo.ParseMethod(m); ok {
			enabled[method] = true
		}
	}
	return &CreateAttempt{booking: b, attempts: a, idem: idem, gateway: g, clock: clk, enabled: enabled, returns: returns}
}

// WithMaxAttempts sets the per-booking attempt cap (0 = no cap).
func (u *CreateAttempt) WithMaxAttempts(n int) *CreateAttempt {
	u.maxAttempts = n
	return u
}

// CreateAttempt returns the attempt with its payment instruction. A repeat with the same Idempotency-Key returns
// the stored result without charging again.
func (u *CreateAttempt) CreateAttempt(ctx context.Context, payer inbound.Payer, bookingID, key string, req dto.CreateAttemptRequest) (dto.Attempt, error) {
	if len(key) < 16 || len(key) > 64 {
		return dto.Attempt{}, inbound.Validation("Idempotency-Key", "Idempotency-Key harus 16..64 karakter")
	}
	method, ok := vo.ParseMethod(req.Method)
	if !ok {
		return dto.Attempt{}, inbound.Validation("method", "metode pembayaran tidak dikenal")
	}
	if !u.enabled[method] {
		return dto.Attempt{}, inbound.ErrMethodUnavailable
	}
	payable, err := loadOwnedPayable(ctx, u.booking, payer, bookingID)
	if err != nil {
		return dto.Attempt{}, err
	}
	if !isPayable(payable, u.clock.Now()) {
		return dto.Attempt{}, inbound.ErrNotPayable
	}

	hash := sha256.Sum256([]byte(payable.BookingID + "|" + string(method)))
	rec, found, err := u.idem.Begin(ctx, scopeCreateAttempt, payer.VisitorID, key, hash[:], idempotencyTTL)
	if err != nil {
		return dto.Attempt{}, fmt.Errorf("%w: %w", inbound.ErrDependency, err)
	}
	if found {
		return replay(rec, hash[:])
	}

	res, err := u.run(ctx, payable, method, key)
	if err != nil {
		// Let the client retry with the same key: the next try repeats the same checks (and the same order, if one exists).
		if rerr := u.idem.Release(context.WithoutCancel(ctx), scopeCreateAttempt, payer.VisitorID, key); rerr != nil {
			slog.WarnContext(ctx, "idempotency release gagal", "error", rerr)
		}
		return dto.Attempt{}, err
	}
	body, _ := json.Marshal(res)
	if cerr := u.idem.Complete(context.WithoutCancel(ctx), scopeCreateAttempt, payer.VisitorID, key, 201, body); cerr != nil {
		slog.WarnContext(ctx, "idempotency complete gagal; attempt sudah dibuat", "error", cerr)
	}
	return res, nil
}

func replay(rec outbound.IdempotencyRecord, hash []byte) (dto.Attempt, error) {
	if !bytes.Equal(rec.RequestHash, hash) {
		return dto.Attempt{}, inbound.ErrIdemReused
	}
	if rec.State != "COMPLETED" {
		return dto.Attempt{}, inbound.ErrDependency // same request still running: the client retries
	}
	var a dto.Attempt
	if err := json.Unmarshal(rec.ResponseBody, &a); err != nil {
		return dto.Attempt{}, fmt.Errorf("%w: respons tersimpan rusak: %w", inbound.ErrDependency, err)
	}
	return a, nil
}

func (u *CreateAttempt) run(ctx context.Context, payable outbound.Payable, method vo.Method, key string) (dto.Attempt, error) {
	// 1. Lock first: no money moves for a booking Booking will not honour (Property 2).
	lock, err := u.booking.Lock(ctx, payable.BookingID, key)
	switch {
	case errors.Is(err, outbound.ErrBookingNotFound):
		return dto.Attempt{}, inbound.ErrNotFound
	case err != nil:
		return dto.Attempt{}, fmt.Errorf("%w: %w", inbound.ErrDependency, err)
	case lock.Outcome != outbound.LockOK:
		return dto.Attempt{}, inbound.ErrNotPayable
	}
	now := u.clock.Now()
	remaining := lock.PaymentExpiresAt.Sub(now)
	if remaining < minChargeExpiry {
		return dto.Attempt{}, inbound.ErrNotPayable
	}

	// 2. Same method with a live instruction: return it, never charge twice. Otherwise make room for the next attempt.
	live, err := u.makeRoom(ctx, payable.BookingID, method, now)
	if err != nil {
		return dto.Attempt{}, err
	}
	if live != nil {
		return *live, nil
	}

	// 3. Store CREATED before the charge so an ambiguous timeout can be reconciled on the same order id.
	a, err := u.attempts.CreateNext(ctx, outbound.NewAttempt{
		BookingID: payable.BookingID, PublicRef: payable.PublicRef, Method: method,
		AmountMinor: lock.AmountMinor, ExpiresAt: lock.PaymentExpiresAt, BookingVersion: lock.Version,
		MaxAttemptNo: u.maxAttempts,
	})
	if err != nil {
		return dto.Attempt{}, createNextError(err)
	}

	// 4. Charge. The provider transaction expires with the booking (Property 5).
	returns := u.returns.fill(a)
	res, cerr := u.gateway.Charge(ctx, outbound.ChargeRequest{
		OrderID: a.ProviderOrderID, Method: method, AmountMinor: a.AmountMinor, OrderTime: now, Expiry: remaining, FinishURL: returns.Success,
	})
	cleanup := context.WithoutCancel(ctx)
	switch {
	case cerr == nil:
		ins := res.Instruction
		ins.SuccessReturnURL, ins.FailedReturnURL = returns.Success, returns.Failed
		if _, err := u.attempts.Transition(cleanup, outbound.TransitionSpec{
			AttemptID: a.ID, To: vo.AttemptPending, ProviderTxID: res.ProviderTxID, Instruction: &ins, LastProviderStatus: "pending",
		}); err != nil {
			return dto.Attempt{}, fmt.Errorf("%w: %w", inbound.ErrDependency, err)
		}
		a.Status, a.ProviderTxID, a.Instruction = vo.AttemptPending, res.ProviderTxID, &ins
		return mapper.AttemptToDTO(a), nil
	case errors.Is(cerr, outbound.ErrGatewayRejected):
		u.closeAttempt(cleanup, a, vo.AttemptFailed, cerr)
	case errors.Is(cerr, outbound.ErrGatewayUnavailable):
		u.closeAttempt(cleanup, a, vo.AttemptCancelled, cerr) // never sent: nothing to reconcile
	default:
		// Ambiguous (timeout, 5xx) or cancelled: Midtrans may have created the order. Never create another order for
		// this attempt; the next check (resolve, webhook, or the customer's retry) uses the same order id.
		u.closeAttempt(cleanup, a, vo.AttemptUnknown, cerr)
	}
	slog.WarnContext(ctx, "charge gagal", "attemptNo", a.AttemptNo, "method", method, "error", cerr)
	return dto.Attempt{}, inbound.ErrProviderUnavailable
}

// makeRoom handles the booking's open attempt before a new one. The same method with a live instruction is returned
// as is. Otherwise the cap is checked BEFORE the open order is closed: refusing after cancelling it would leave the
// buyer with nothing to pay. A nil attempt and nil error mean "go on and create the next attempt".
func (u *CreateAttempt) makeRoom(ctx context.Context, bookingID string, method vo.Method, now time.Time) (*dto.Attempt, error) {
	open, found, err := u.attempts.FindOpen(ctx, bookingID)
	if err != nil {
		return nil, fmt.Errorf("%w: %w", inbound.ErrDependency, err)
	}
	if found && open.Method == method && open.Status == vo.AttemptPending && open.Instruction != nil {
		live := mapper.AttemptToDTO(open)
		return &live, nil
	}
	if err := u.checkAttemptLimit(ctx, bookingID); err != nil {
		return nil, err
	}
	if found {
		return nil, u.closeOpen(ctx, open, now)
	}
	return nil, nil
}

// createNextError maps a CreateNext failure. ErrOpenAttemptExists means a parallel request won: retryable 503, like
// any storage error. ErrAttemptLimit is the cap hit inside the INSERT by a request that raced past the pre-check.
func createNextError(err error) error {
	if errors.Is(err, outbound.ErrAttemptLimit) {
		return inbound.ErrAttemptLimit
	}
	return fmt.Errorf("%w: %w", inbound.ErrDependency, err)
}

// checkAttemptLimit refuses a new attempt once the booking has maxAttempts of them. attempt_no is 1, 2, ... per
// booking, so the latest number is the count. This pre-check runs before the open order is closed (good UX); the same
// cap is enforced atomically inside CreateNext for requests racing past it.
func (u *CreateAttempt) checkAttemptLimit(ctx context.Context, bookingID string) error {
	if u.maxAttempts <= 0 {
		return nil
	}
	latest, found, err := u.attempts.FindLatest(ctx, bookingID)
	if err != nil {
		return fmt.Errorf("%w: %w", inbound.ErrDependency, err)
	}
	if found && latest.AttemptNo >= u.maxAttempts {
		return inbound.ErrAttemptLimit
	}
	return nil
}

func (u *CreateAttempt) closeAttempt(ctx context.Context, a entity.Attempt, to vo.AttemptStatus, cause error) {
	if _, err := u.attempts.Transition(ctx, outbound.TransitionSpec{AttemptID: a.ID, To: to, LastProviderStatus: fmt.Sprintf("charge_%s", to)}); err != nil {
		slog.ErrorContext(ctx, "status attempt gagal disimpan setelah charge gagal", "attemptNo", a.AttemptNo, "to", to, "error", err, "cause", cause)
	}
}

// closeOpen ends the booking's previous attempt before a new one starts. It asks Midtrans first: if the old order
// was already paid, a new attempt must not start (the booking will settle).
func (u *CreateAttempt) closeOpen(ctx context.Context, old entity.Attempt, now time.Time) error {
	st, err := u.gateway.Status(ctx, old.ProviderOrderID)
	switch {
	case errors.Is(err, outbound.ErrOrderNotFound):
		return u.moveTo(ctx, old, vo.AttemptCancelled) // Midtrans never saw this order
	case err != nil:
		return fmt.Errorf("%w: %w", inbound.ErrProviderUnavailable, err)
	}
	switch st.Status {
	case vo.AttemptSucceeded:
		if _, err := applySuccess(ctx, u.attempts, old, st.ProviderTxID, st.RawStatus, st.SettlementTime, now); err != nil {
			return fmt.Errorf("%w: %w", inbound.ErrDependency, err)
		}
		return inbound.ErrNotPayable
	case vo.AttemptPending:
		if err := u.gateway.Cancel(ctx, old.ProviderOrderID); err != nil && !errors.Is(err, outbound.ErrOrderNotFound) {
			return fmt.Errorf("%w: %w", inbound.ErrProviderUnavailable, err)
		}
		return u.moveTo(ctx, old, vo.AttemptCancelled)
	default: // failed, cancelled, expired at Midtrans
		return u.moveTo(ctx, old, st.Status)
	}
}

func (u *CreateAttempt) moveTo(ctx context.Context, a entity.Attempt, to vo.AttemptStatus) error {
	if _, err := u.attempts.Transition(ctx, outbound.TransitionSpec{AttemptID: a.ID, To: to, LastProviderStatus: "replaced"}); err != nil {
		return fmt.Errorf("%w: %w", inbound.ErrDependency, err)
	}
	return nil
}
