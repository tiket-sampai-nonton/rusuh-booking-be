package postgres

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/booking/internal/booking/port/outbound"
)

// FunnelStats implements outbound.IFunnelStats: row counts per event and status, read at scrape time (NFR-09), plus
// event names. Only event ids, names and status columns are read; no buyer data leaves the database.
type FunnelStats struct{ pool *pgxpool.Pool }

// NewFunnelStats accepts the pool and returns the concrete reader.
func NewFunnelStats(pool *pgxpool.Pool) *FunnelStats { return &FunnelStats{pool: pool} }

// AdmissionCounts groups admissions by event, status, and end reason ("" while active).
func (s *FunnelStats) AdmissionCounts(ctx context.Context) ([]outbound.FunnelCount, error) {
	return s.counts(ctx, `SELECT event_id::text, status, COALESCE(end_reason, ''), count(*)
		FROM booking.admissions GROUP BY 1, 2, 3`)
}

// BookingCounts groups bookings by event and status.
func (s *FunnelStats) BookingCounts(ctx context.Context) ([]outbound.FunnelCount, error) {
	return s.counts(ctx, `SELECT event_id::text, status, '', count(*) FROM booking.bookings GROUP BY 1, 2`)
}

// EventNames lists id and name of every event. Names are not personal data and /metrics is internal-only.
func (s *FunnelStats) EventNames(ctx context.Context) ([]outbound.EventName, error) {
	rows, err := s.pool.Query(ctx, `SELECT id::text, name FROM booking.events`)
	if err != nil {
		return nil, fmt.Errorf("funnel stats: %w", err)
	}
	defer rows.Close()
	var out []outbound.EventName
	for rows.Next() {
		var n outbound.EventName
		if err := rows.Scan(&n.EventID, &n.Name); err != nil {
			return nil, fmt.Errorf("funnel stats: %w", err)
		}
		out = append(out, n)
	}
	return out, rows.Err()
}

func (s *FunnelStats) counts(ctx context.Context, sql string) ([]outbound.FunnelCount, error) {
	rows, err := s.pool.Query(ctx, sql)
	if err != nil {
		return nil, fmt.Errorf("funnel stats: %w", err)
	}
	defer rows.Close()
	var out []outbound.FunnelCount
	for rows.Next() {
		var c outbound.FunnelCount
		if err := rows.Scan(&c.EventID, &c.Status, &c.EndReason, &c.Count); err != nil {
			return nil, fmt.Errorf("funnel stats: %w", err)
		}
		out = append(out, c)
	}
	return out, rows.Err()
}
