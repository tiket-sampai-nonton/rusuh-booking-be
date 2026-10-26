package postgres

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/tiket-sampai-nonton/rusuh-booking-be/pkg/telemetry"

	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/payment/internal/payment/port/outbound"
)

// webhooksTotal counts Midtrans notifications by processing result (PROCESSED, DUPLICATE, REJECTED, ERROR).
var webhooksTotal = telemetry.NewCounterVec("rbt_payment_webhooks_total", "Notifikasi webhook Midtrans per hasil pemrosesan.", "result")

// Callbacks implements outbound.ICallbackStore.
type Callbacks struct{ pool *pgxpool.Pool }

// NewCallbacks returns the store.
func NewCallbacks(pool *pgxpool.Pool) *Callbacks { return &Callbacks{pool: pool} }

// Record stores one webhook delivery. The same (provider, fingerprint) is kept once; inserted=false means a duplicate.
// Only the digest of the payload is stored, never the raw body.
func (s *Callbacks) Record(ctx context.Context, cb outbound.Callback) (bool, error) {
	tag, err := s.pool.Exec(ctx, `
		INSERT INTO payment_callbacks (provider, fingerprint, provider_order_id, normalized_status, signature_valid,
		                               amount_minor, payload_digest, processing_result)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
		ON CONFLICT (provider, fingerprint) DO NOTHING`,
		cb.Provider, cb.Fingerprint, cb.ProviderOrderID, cb.NormalizedStatus, cb.SignatureValid, cb.AmountMinor, cb.PayloadDigest, cb.Result)
	if err != nil {
		return false, fmt.Errorf("callbacks: record: %w", err)
	}
	inserted := tag.RowsAffected() == 1
	result := cb.Result
	if !inserted {
		result = outbound.CallbackDuplicate // the same notification was recorded before
	}
	webhooksTotal.WithLabelValues(result).Inc()
	return inserted, nil
}
