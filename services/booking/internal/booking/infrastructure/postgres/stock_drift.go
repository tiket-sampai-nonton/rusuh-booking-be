package postgres

import (
	"context"
	_ "embed"
	"fmt"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/booking/internal/booking/port/outbound"
)

// stockDriftSQL is the one definition of the drift query (D-C9.1). See stock_drift.sql for the kinds and the delta rule.
//
//go:embed stock_drift.sql
var stockDriftSQL string

// DriftStats implements outbound.IStockDrift by recounting the stock from the live rows in PostgreSQL.
type DriftStats struct{ pool *pgxpool.Pool }

// NewDriftStats accepts the pool and returns the concrete reader.
func NewDriftStats(pool *pgxpool.Pool) *DriftStats { return &DriftStats{pool: pool} }

// StockDrift returns every counter that disagrees with its recount. An empty slice means the stock is consistent.
func (s *DriftStats) StockDrift(ctx context.Context) ([]outbound.StockDrift, error) {
	rows, err := s.pool.Query(ctx, stockDriftSQL)
	if err != nil {
		return nil, fmt.Errorf("stock drift: %w", err)
	}
	defer rows.Close()
	var out []outbound.StockDrift
	for rows.Next() {
		var d outbound.StockDrift
		if err := rows.Scan(&d.EventID, &d.Kind, &d.Delta); err != nil {
			return nil, fmt.Errorf("stock drift: %w", err)
		}
		out = append(out, d)
	}
	return out, rows.Err()
}
