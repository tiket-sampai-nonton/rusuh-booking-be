package outbound

import (
	"context"
	"errors"
	"time"

	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/payment/internal/payment/domain/entity"
	vo "github.com/tiket-sampai-nonton/rusuh-booking-be/services/payment/internal/payment/domain/valueobject"
)

//go:generate go tool mockgen -source=attempt_repository.go -destination=../../../../_mock/payment/port/outbound/attempt_repository_mock.go -package=mock_outbound

// ErrOpenAttemptExists means the booking already has an open attempt (uq_attempt_open_per_booking).
var ErrOpenAttemptExists = errors.New("attempt: booking sudah punya attempt terbuka")

// NewAttempt is a CREATED attempt to insert. The repository picks attempt_no and builds the order id.
type NewAttempt struct {
	BookingID      string
	PublicRef      string
	Method         vo.Method
	AmountMinor    int64
	ExpiresAt      time.Time
	BookingVersion int
}

// Event is an integration event written to the outbox in the same transaction as a status change.
type Event struct {
	RoutingKey string
	Payload    any
}

// TransitionSpec moves one attempt to a new status. The UPDATE only applies when the current status is one
// the state machine allows to reach To, so a duplicate or out-of-order change is a no-op (applied=false).
type TransitionSpec struct {
	AttemptID          string
	To                 vo.AttemptStatus
	ProviderTxID       string
	SettlementTime     *time.Time
	LastProviderStatus string
	Instruction        *entity.Instruction
	Event              *Event // written to the outbox only when the transition applied
}

// IAttemptRepository stores payment attempts. payment_attempts is the source of truth for status.
type IAttemptRepository interface {
	// CreateNext inserts the next attempt (CREATED). ErrOpenAttemptExists when one is already open.
	CreateNext(ctx context.Context, n NewAttempt) (entity.Attempt, error)
	FindOpen(ctx context.Context, bookingID string) (entity.Attempt, bool, error)
	FindLatest(ctx context.Context, bookingID string) (entity.Attempt, bool, error)
	FindByOrderID(ctx context.Context, orderID string) (entity.Attempt, bool, error)
	// Transition applies spec atomically with its outbox event. applied=false means nothing changed.
	Transition(ctx context.Context, spec TransitionSpec) (applied bool, err error)
}
