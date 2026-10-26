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

// PageTokens implements outbound.IPageTokenStore. Only the SHA-256 of a token is stored.
type PageTokens struct{ pool *pgxpool.Pool }

// NewPageTokens returns the store.
func NewPageTokens(pool *pgxpool.Pool) *PageTokens { return &PageTokens{pool: pool} }

// Create stores a new token hash.
func (s *PageTokens) Create(ctx context.Context, tokenHash []byte, bookingID, visitorID string, expiresAt time.Time) error {
	if _, err := s.pool.Exec(ctx, `
		INSERT INTO payment_page_tokens (token_hash, booking_id, visitor_id, expires_at)
		VALUES ($1, $2::uuid, $3::uuid, $4)`, tokenHash, bookingID, visitorID, expiresAt); err != nil {
		return fmt.Errorf("page tokens: create: %w", err)
	}
	return nil
}

// Consume marks the token used in one atomic UPDATE, so a replayed token finds nothing to update.
func (s *PageTokens) Consume(ctx context.Context, tokenHash []byte, now time.Time) (outbound.PageSession, error) {
	var ps outbound.PageSession
	err := s.pool.QueryRow(ctx, `
		UPDATE payment_page_tokens SET used_at = $2
		 WHERE token_hash = $1 AND used_at IS NULL AND expires_at > $2
		RETURNING booking_id::text, visitor_id::text`, tokenHash, now).Scan(&ps.BookingID, &ps.VisitorID)
	if errors.Is(err, pgx.ErrNoRows) {
		return outbound.PageSession{}, outbound.ErrPageTokenInvalid
	}
	if err != nil {
		return outbound.PageSession{}, fmt.Errorf("page tokens: consume: %w", err)
	}
	return ps, nil
}
