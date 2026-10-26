package usecase

import (
	"context"
	"errors"
	"fmt"

	"github.com/tiket-sampai-nonton/rusuh-booking-be/pkg/clock"

	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/payment/internal/payment/domain/dto"
	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/payment/internal/payment/domain/entity"
	vo "github.com/tiket-sampai-nonton/rusuh-booking-be/services/payment/internal/payment/domain/valueobject"
	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/payment/internal/payment/port/inbound"
	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/payment/internal/payment/port/outbound"
)

// ResolvePayment answers Booking before it expires a booking (design §9.3): was it paid, and if not, close the
// order at Midtrans. It never blocks: when Midtrans cannot answer the outcome is UNKNOWN and Booking keeps the stock.
type ResolvePayment struct {
	attempts outbound.IAttemptRepository
	gateway  outbound.IPaymentGateway
	clock    clock.IClock
}

// NewResolvePayment wires the usecase.
func NewResolvePayment(a outbound.IAttemptRepository, g outbound.IPaymentGateway, clk clock.IClock) *ResolvePayment {
	return &ResolvePayment{attempts: a, gateway: g, clock: clk}
}

// ResolvePayment checks (and with CHECK_AND_CANCEL, cancels) the booking's latest attempt.
func (u *ResolvePayment) ResolvePayment(ctx context.Context, bookingID, mode string) (dto.ResolveResponse, error) {
	if !isUUID(bookingID) {
		return dto.ResolveResponse{}, inbound.Validation("bookingId", "bookingId harus UUID")
	}
	if mode != dto.ResolveCheck && mode != dto.ResolveCheckAndCancel {
		return dto.ResolveResponse{}, inbound.Validation("mode", "mode harus CHECK atau CHECK_AND_CANCEL")
	}
	a, found, err := u.attempts.FindLatest(ctx, bookingID)
	if err != nil {
		return dto.ResolveResponse{}, fmt.Errorf("%w: %w", inbound.ErrDependency, err)
	}
	if !found {
		return dto.ResolveResponse{Outcome: dto.OutcomeNoAttempt}, nil
	}
	switch a.Status {
	case vo.AttemptSucceeded:
		return succeeded(a), nil
	case vo.AttemptFailed:
		return dto.ResolveResponse{Outcome: dto.OutcomeNotPaid, AttemptID: a.ID}, nil
	}

	st, err := u.gateway.Status(ctx, a.ProviderOrderID)
	switch {
	case errors.Is(err, outbound.ErrOrderNotFound): // Midtrans never saw it: nothing can have been paid
		u.persist(ctx, a, vo.AttemptCancelled, outbound.GatewayStatus{})
		return dto.ResolveResponse{Outcome: dto.OutcomeNotPaid, AttemptID: a.ID}, nil
	case err != nil:
		return dto.ResolveResponse{Outcome: dto.OutcomeUnknown, AttemptID: a.ID}, nil
	}
	if st.Status == vo.AttemptSucceeded {
		return u.settle(ctx, a, st)
	}
	if mode == dto.ResolveCheckAndCancel && st.Status == vo.AttemptPending {
		return u.cancel(ctx, a)
	}
	if st.Status != vo.AttemptPending {
		u.persist(ctx, a, st.Status, st)
	}
	return dto.ResolveResponse{Outcome: dto.OutcomeNotPaid, AttemptID: a.ID}, nil
}

// cancel closes the order at Midtrans and checks again, because it may have been paid while we were cancelling.
func (u *ResolvePayment) cancel(ctx context.Context, a entity.Attempt) (dto.ResolveResponse, error) {
	cerr := u.gateway.Cancel(ctx, a.ProviderOrderID)
	if cerr != nil && !errors.Is(cerr, outbound.ErrOrderNotFound) && !errors.Is(cerr, outbound.ErrGatewayRejected) {
		return dto.ResolveResponse{Outcome: dto.OutcomeUnknown, AttemptID: a.ID}, nil
	}
	st, err := u.gateway.Status(ctx, a.ProviderOrderID)
	switch {
	case errors.Is(err, outbound.ErrOrderNotFound):
		u.persist(ctx, a, vo.AttemptCancelled, outbound.GatewayStatus{})
		return dto.ResolveResponse{Outcome: dto.OutcomeNotPaid, AttemptID: a.ID}, nil
	case err != nil:
		return dto.ResolveResponse{Outcome: dto.OutcomeUnknown, AttemptID: a.ID}, nil
	}
	switch st.Status {
	case vo.AttemptSucceeded:
		return u.settle(ctx, a, st)
	case vo.AttemptPending:
		// Cancel was refused and the order is still open: not confirmed closed, so Booking must not release stock.
		return dto.ResolveResponse{Outcome: dto.OutcomeUnknown, AttemptID: a.ID}, nil
	}
	u.persist(ctx, a, st.Status, st)
	return dto.ResolveResponse{Outcome: dto.OutcomeNotPaid, AttemptID: a.ID}, nil
}

func (u *ResolvePayment) settle(ctx context.Context, a entity.Attempt, st outbound.GatewayStatus) (dto.ResolveResponse, error) {
	if _, err := applySuccess(ctx, u.attempts, a, st.ProviderTxID, st.RawStatus, st.SettlementTime, u.clock.Now()); err != nil {
		return dto.ResolveResponse{Outcome: dto.OutcomeUnknown, AttemptID: a.ID}, nil // not stored yet: Booking asks again
	}
	latest, found, err := u.attempts.FindLatest(ctx, a.BookingID)
	if err != nil || !found {
		return dto.ResolveResponse{Outcome: dto.OutcomeUnknown, AttemptID: a.ID}, nil
	}
	return succeeded(latest), nil
}

// persist records a final provider status on the attempt. A failure is not fatal: the outcome is still correct.
func (u *ResolvePayment) persist(ctx context.Context, a entity.Attempt, to vo.AttemptStatus, st outbound.GatewayStatus) {
	if to == vo.AttemptFailed {
		_, _ = applyFailure(ctx, u.attempts, a, st.RawStatus)
		return
	}
	_, _ = u.attempts.Transition(ctx, outbound.TransitionSpec{AttemptID: a.ID, To: to, ProviderTxID: st.ProviderTxID, LastProviderStatus: st.RawStatus})
}

func succeeded(a entity.Attempt) dto.ResolveResponse {
	out := dto.ResolveResponse{Outcome: dto.OutcomeSucceeded, AttemptID: a.ID}
	if a.SettlementTime != nil {
		t := a.SettlementTime.UTC()
		out.SettlementTime = &t
	}
	return out
}
