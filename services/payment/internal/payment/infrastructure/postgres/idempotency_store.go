package postgres

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/payment/internal/payment/port/outbound"
)

// Idempotency implements outbound.IIdempotencyStore.
type Idempotency struct{ pool *pgxpool.Pool }

// NewIdempotency returns the store.
func NewIdempotency(pool *pgxpool.Pool) *Idempotency { return &Idempotency{pool: pool} }

// Begin claims the key. found=true returns the existing record (a repeat of the same request, or a key reused
// for a different one). An expired record is replaced.
func (s *Idempotency) Begin(ctx context.Context, scope, ownerID, key string, requestHash []byte, ttl time.Duration) (outbound.IdempotencyRecord, bool, error) {
	if _, err := s.pool.Exec(ctx, `
		DELETE FROM idempotency_records
		 WHERE scope = $1 AND owner_id = $2::uuid AND idempotency_key = $3 AND expires_at < now()`, scope, ownerID, key); err != nil {
		return outbound.IdempotencyRecord{}, false, fmt.Errorf("idempotency: expire: %w", err)
	}
	tag, err := s.pool.Exec(ctx, `
		INSERT INTO idempotency_records (scope, owner_id, idempotency_key, request_hash, state, expires_at)
		VALUES ($1, $2::uuid, $3, $4, 'IN_PROGRESS', now() + make_interval(secs => $5))
		ON CONFLICT (scope, owner_id, idempotency_key) DO NOTHING`, scope, ownerID, key, requestHash, ttl.Seconds())
	if err != nil {
		return outbound.IdempotencyRecord{}, false, fmt.Errorf("idempotency: begin: %w", err)
	}
	if tag.RowsAffected() == 1 {
		return outbound.IdempotencyRecord{State: "IN_PROGRESS", RequestHash: requestHash}, false, nil
	}
	var rec outbound.IdempotencyRecord
	err = s.pool.QueryRow(ctx, `
		SELECT state, request_hash, response_body
		  FROM idempotency_records WHERE scope = $1 AND owner_id = $2::uuid AND idempotency_key = $3`,
		scope, ownerID, key).Scan(&rec.State, &rec.RequestHash, &rec.ResponseBody)
	if errors.Is(err, pgx.ErrNoRows) { // released between the insert and the select: let the caller retry
		return outbound.IdempotencyRecord{State: "IN_PROGRESS", RequestHash: requestHash}, true, nil
	}
	if err != nil {
		return outbound.IdempotencyRecord{}, false, fmt.Errorf("idempotency: load: %w", err)
	}
	return rec, true, nil
}

// Complete stores the final response so a repeat returns it without charging again.
func (s *Idempotency) Complete(ctx context.Context, scope, ownerID, key string, status int, body []byte) error {
	if _, err := s.pool.Exec(ctx, `
		UPDATE idempotency_records SET state = 'COMPLETED', response_status = $4, response_body = $5
		 WHERE scope = $1 AND owner_id = $2::uuid AND idempotency_key = $3`, scope, ownerID, key, status, body); err != nil {
		return fmt.Errorf("idempotency: complete: %w", err)
	}
	return nil
}

// Release drops an unfinished claim so the client can retry with the same key after a failure.
func (s *Idempotency) Release(ctx context.Context, scope, ownerID, key string) error {
	if _, err := s.pool.Exec(ctx, `
		DELETE FROM idempotency_records
		 WHERE scope = $1 AND owner_id = $2::uuid AND idempotency_key = $3 AND state = 'IN_PROGRESS'`, scope, ownerID, key); err != nil {
		return fmt.Errorf("idempotency: release: %w", err)
	}
	return nil
}
