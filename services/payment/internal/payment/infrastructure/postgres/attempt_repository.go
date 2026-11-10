// Package postgres implements the Payment repositories on schema payment (pgx). Tables are resolved through
// the connection search_path, like the migrations.
package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/tiket-sampai-nonton/rusuh-booking-be/pkg/outbox"
	pkgpg "github.com/tiket-sampai-nonton/rusuh-booking-be/pkg/postgres"
	"github.com/tiket-sampai-nonton/rusuh-booking-be/pkg/telemetry"

	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/payment/internal/payment/domain/entity"
	vo "github.com/tiket-sampai-nonton/rusuh-booking-be/services/payment/internal/payment/domain/valueobject"
	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/payment/internal/payment/port/outbound"
)

// attemptsTotal counts attempts by method and the status they reached. Conversion is SUCCEEDED / CREATED and the
// expiry rate EXPIRED / CREATED (NFR-09). Labels are two small enums; no booking or order ids.
var attemptsTotal = telemetry.NewCounterVec("rbt_payment_attempts_total", "Attempt pembayaran per metode dan status yang dicapai.", "method", "status")

// Attempts implements outbound.IAttemptRepository.
type Attempts struct{ pool *pgxpool.Pool }

// NewAttempts returns the repository.
func NewAttempts(pool *pgxpool.Pool) *Attempts { return &Attempts{pool: pool} }

const attemptCols = `id::text, booking_id::text, attempt_no, method, provider_order_id, COALESCE(provider_transaction_id, ''),
	status, amount_minor, expires_at, instruction, settlement_time, COALESCE(last_provider_status, ''), booking_version`

func scanAttempt(row pgx.Row) (entity.Attempt, error) {
	var a entity.Attempt
	var method, status string
	var instruction []byte
	if err := row.Scan(&a.ID, &a.BookingID, &a.AttemptNo, &method, &a.ProviderOrderID, &a.ProviderTxID,
		&status, &a.AmountMinor, &a.ExpiresAt, &instruction, &a.SettlementTime, &a.LastProviderStatus, &a.BookingVersion); err != nil {
		return entity.Attempt{}, err
	}
	a.Method, a.Status = vo.Method(method), vo.AttemptStatus(status)
	if len(instruction) > 0 {
		var ins entity.Instruction
		if err := json.Unmarshal(instruction, &ins); err != nil {
			return entity.Attempt{}, fmt.Errorf("attempts: instruction: %w", err)
		}
		a.Instruction = &ins
	}
	return a, nil
}

// CreateNext inserts the next attempt as CREATED. The attempt number and the order id are decided in one
// statement. Two requests racing for the same booking end in a unique violation (attempt_no or the open-attempt
// index): exactly one wins, the other gets ErrOpenAttemptExists (Property 1).
func (r *Attempts) CreateNext(ctx context.Context, n outbound.NewAttempt) (entity.Attempt, error) {
	a, err := scanAttempt(r.pool.QueryRow(ctx, `
		WITH next AS (SELECT COALESCE(MAX(attempt_no), 0) + 1 AS no FROM payment_attempts WHERE booking_id = $1::uuid)
		INSERT INTO payment_attempts (booking_id, attempt_no, method, provider, provider_order_id, status, amount_minor, expires_at, booking_version)
		SELECT $1::uuid, next.no, $2, $3, $4 || '-' || next.no, 'CREATED', $5, $6, $7 FROM next
		RETURNING `+attemptCols,
		n.BookingID, string(n.Method), entity.Provider, n.PublicRef, n.AmountMinor, n.ExpiresAt, n.BookingVersion))
	if pkgpg.IsCode(err, pkgpg.CodeUniqueViolation) {
		return entity.Attempt{}, outbound.ErrOpenAttemptExists
	}
	if err != nil {
		return entity.Attempt{}, fmt.Errorf("attempts: create: %w", err)
	}
	attemptsTotal.WithLabelValues(string(a.Method), string(a.Status)).Inc()
	return a, nil
}

func (r *Attempts) find(ctx context.Context, where string, arg any) (entity.Attempt, bool, error) {
	a, err := scanAttempt(r.pool.QueryRow(ctx, `SELECT `+attemptCols+` FROM payment_attempts WHERE `+where, arg))
	if errors.Is(err, pgx.ErrNoRows) || pkgpg.IsCode(err, "22P02") { // 22P02: id is not a UUID
		return entity.Attempt{}, false, nil
	}
	if err != nil {
		return entity.Attempt{}, false, fmt.Errorf("attempts: find: %w", err)
	}
	return a, true, nil
}

// FindOpen returns the booking's open attempt (CREATED, PENDING, or UNKNOWN), if any.
func (r *Attempts) FindOpen(ctx context.Context, bookingID string) (entity.Attempt, bool, error) {
	return r.find(ctx, `booking_id = $1::uuid AND status IN ('CREATED','PENDING','UNKNOWN')`, bookingID)
}

// FindLatest returns the booking's newest attempt of any status.
func (r *Attempts) FindLatest(ctx context.Context, bookingID string) (entity.Attempt, bool, error) {
	return r.find(ctx, `booking_id = $1::uuid ORDER BY attempt_no DESC LIMIT 1`, bookingID)
}

// FindByOrderID returns the attempt for a provider order id.
func (r *Attempts) FindByOrderID(ctx context.Context, orderID string) (entity.Attempt, bool, error) {
	return r.find(ctx, `provider_order_id = $1`, orderID)
}

// Transition applies spec with an UPDATE guarded by the state machine. The event, if any, is written to the
// outbox in the same transaction, so money changes and their event commit together (invariant I-6).
func (r *Attempts) Transition(ctx context.Context, spec outbound.TransitionSpec) (bool, error) {
	from := vo.SourcesOf(spec.To)
	sources := make([]string, len(from))
	for i, s := range from {
		sources[i] = string(s)
	}
	var instruction []byte
	if spec.Instruction != nil {
		var err error
		if instruction, err = json.Marshal(spec.Instruction); err != nil {
			return false, fmt.Errorf("attempts: instruction: %w", err)
		}
	}

	applied := false
	appliedMethod := ""
	err := pkgpg.WithTx(ctx, r.pool, pgx.TxOptions{}, func(tx pgx.Tx) error {
		var bookingID, method string
		err := tx.QueryRow(ctx, `
			UPDATE payment_attempts
			   SET status = $2,
			       provider_transaction_id = COALESCE(NULLIF($3, ''), provider_transaction_id),
			       settlement_time = COALESCE($4, settlement_time),
			       last_provider_status = COALESCE(NULLIF($5, ''), last_provider_status),
			       instruction = COALESCE($6::jsonb, instruction),
			       updated_at = now()
			 WHERE id = $1::uuid AND status = ANY($7::text[])
			RETURNING booking_id::text, method`,
			spec.AttemptID, string(spec.To), spec.ProviderTxID, spec.SettlementTime, spec.LastProviderStatus, instruction, sources).Scan(&bookingID, &method)
		if errors.Is(err, pgx.ErrNoRows) {
			return nil // already in this status, or the move is not allowed
		}
		if err != nil {
			return err
		}
		applied = true
		appliedMethod = method
		if spec.Event == nil {
			return nil
		}
		return outbox.Write(ctx, tx, outbox.Message{RoutingKey: spec.Event.RoutingKey, AggregateID: bookingID, Payload: spec.Event.Payload})
	})
	if err != nil {
		return false, fmt.Errorf("attempts: transition: %w", err)
	}
	if applied {
		attemptsTotal.WithLabelValues(appliedMethod, string(spec.To)).Inc()
	}
	return applied, nil
}
