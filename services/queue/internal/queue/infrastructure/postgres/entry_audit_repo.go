package postgres

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/queue/internal/queue/port/outbound"
)

// EntryAudit implements outbound.IEntryAuditRepo (append-only table, design §3.3).
type EntryAudit struct{ pool *pgxpool.Pool }

// NewEntryAudit returns the repository.
func NewEntryAudit(pool *pgxpool.Pool) *EntryAudit { return &EntryAudit{pool: pool} }

// Append inserts one audit row. account_id is NULL for guests ("").
func (r *EntryAudit) Append(ctx context.Context, e outbound.AuditRecord) error {
	meta := e.Metadata
	if meta == nil {
		meta = map[string]any{}
	}
	metaJSON, err := json.Marshal(meta)
	if err != nil {
		return fmt.Errorf("entry_audit: marshal metadata: %w", err)
	}
	var accountID any
	if e.AccountID != "" {
		accountID = e.AccountID
	}
	_, err = r.pool.Exec(ctx, `
		INSERT INTO entry_audit (event_id, entry_id, seq, buyer_nik_hash, action, occurred_at, metadata, account_id)
		VALUES ($1::uuid, $2::uuid, $3, $4, $5, $6, $7, $8)`,
		e.EventID, e.EntryID, e.Seq, e.BuyerNikHash, e.Action, e.OccurredAt, metaJSON, accountID)
	if err != nil {
		return fmt.Errorf("entry_audit: append: %w", err)
	}
	return nil
}
