//go:build integration

package postgres

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/require"

	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/booking/internal/booking/port/outbound"
)

// benchHoldWorkers is the number of concurrent holds; they all queue on the one event_supply row.
const benchHoldWorkers = 16

// BenchmarkHold_OneEvent measures the hold of one event under contention: 16 workers, each op is one hold of a
// fresh admission. Every hold takes the supply row lock, so ns/op is about the time the supply row stays locked
// (C7.1). Run: go.sh test -tags integration -run '^$' -bench BenchmarkHold_OneEvent -benchtime 2000x ./internal/booking/infrastructure/postgres/
func BenchmarkHold_OneEvent(b *testing.B) {
	pool := setup(b)
	eventID, catID := benchSeedEvent(b, pool, b.N)
	uow, err := NewUnitOfWork(pool)
	require.NoError(b, err)
	holds := benchAdmissions(b, pool, eventID, b.N)

	var next atomic.Int64
	var wg sync.WaitGroup
	b.ResetTimer()
	start := time.Now()
	for w := 0; w < benchHoldWorkers; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			ctx := context.Background()
			for {
				i := int(next.Add(1) - 1)
				if i >= b.N {
					return
				}
				h := holds[i]
				spec := outbound.HoldSpec{
					BookingID: newTestUUID(), PublicRef: fmt.Sprintf("BM%08d", i), EventID: eventID,
					AdmissionID: h.admissionID, VisitorID: h.visitorID, BuyerNIKHash: h.buyerHash,
					BuyerEmailEnc: sealedEmail, AmountMinor: 150000, PaymentExpiresAt: time.Now().Add(10 * time.Minute),
					Lines: []outbound.HoldLine{{
						ItemID: newTestUUID(), CategoryID: catID, UnitPriceMinor: 150000, AttendeeNIKHash: h.attendeeHash, AttendeeNIKEnc: h.attendeeHash,
						AttendeeNIKMasked: "3174********0001", AttendeeName: "Budi Santoso",
					}},
					CorrelationID: "bench",
				}
				if _, err := uow.Hold(ctx, spec); err != nil {
					b.Errorf("hold %d: %v", i, err)
				}
			}
		}()
	}
	wg.Wait()
	elapsed := time.Since(start)
	b.StopTimer()
	b.ReportMetric(float64(b.N)/elapsed.Seconds(), "holds/s")
}

type benchHold struct {
	admissionID, visitorID  string
	buyerHash, attendeeHash []byte
}

// benchSeedEvent creates one event whose category and supply hold n units, with a single allocated unit per admission.
func benchSeedEvent(b *testing.B, pool *pgxpool.Pool, n int) (eventID, catID string) {
	b.Helper()
	ctx := context.Background()
	require.NoError(b, pool.QueryRow(ctx, `
		INSERT INTO booking.events (slug, name, venue, starts_at, sales_starts_at, sales_ends_at, status, genre, published_at)
		VALUES ('bench-konser', 'Konser Bench', 'GBK', '2026-12-25T19:00:00Z', now() - interval '1 hour', now() + interval '1 day', 'AVAILABLE', ARRAY['POP'], now())
		RETURNING id::text`).Scan(&eventID))
	require.NoError(b, pool.QueryRow(ctx, `
		INSERT INTO booking.ticket_categories (event_id, code, name, price_minor) VALUES ($1::uuid, 'VIP', 'VIP', 150000) RETURNING id::text`,
		eventID).Scan(&catID))
	_, err := pool.Exec(ctx, `INSERT INTO booking.category_inventories (category_id, event_id, capacity) VALUES ($1::uuid, $2::uuid, $3)`, catID, eventID, n)
	require.NoError(b, err)
	_, err = pool.Exec(ctx, `INSERT INTO booking.event_supply (event_id, total_capacity, allocated_units) VALUES ($1::uuid, $2, $2)`, eventID, n)
	require.NoError(b, err)
	return eventID, catID
}

// benchAdmissions creates n ACTIVE admissions with one reserved unit each, edit disabled (the locked hold path).
func benchAdmissions(b *testing.B, pool *pgxpool.Pool, eventID string, n int) []benchHold {
	b.Helper()
	ctx := context.Background()
	_, err := pool.Exec(ctx, `
		INSERT INTO booking.admissions (event_id, queue_entry_id, queue_seq, visitor_id, buyer_nik_hash, buyer_email_enc, units, outstanding_units, status,
			cfg_selection_ttl_seconds, cfg_payment_ttl_seconds, cfg_submit_grace_seconds, cfg_edit_enabled, cfg_max_edits, cfg_allow_mixed_categories,
			claimed_at, expires_at)
		SELECT $1::uuid, gen_random_uuid(), g, gen_random_uuid(), convert_to('buyer' || lpad(g::text, 8, '0'), 'UTF8'), $3, 1, 1, 'ACTIVE',
			300, 600, 5, false, 3, true, now(), now() + interval '1 hour'
		  FROM generate_series(1, $2::int) g`, eventID, n, sealedEmail)
	require.NoError(b, err)
	rows, err := pool.Query(ctx, `SELECT id::text, visitor_id::text, queue_seq FROM booking.admissions WHERE event_id=$1::uuid ORDER BY queue_seq`, eventID)
	require.NoError(b, err)
	defer rows.Close()
	out := make([]benchHold, 0, n)
	for rows.Next() {
		var h benchHold
		var seq int
		require.NoError(b, rows.Scan(&h.admissionID, &h.visitorID, &seq))
		h.buyerHash = []byte(fmt.Sprintf("buyer%08d", seq))
		h.attendeeHash = []byte(fmt.Sprintf("holder%08d", seq))
		out = append(out, h)
	}
	require.NoError(b, rows.Err())
	require.Len(b, out, n)
	return out
}

// BenchmarkReserveBatch_100 measures one 100-entry reservation batch per op on one event. Every entry is granted
// (3 units each), so the op is the full grant path: one supply lock, one aggregate supply update, one bulk insert (C7.2).
// Run: go.sh test -tags integration -run '^$' -bench BenchmarkReserveBatch_100 -benchtime 50x ./internal/booking/infrastructure/postgres/
func BenchmarkReserveBatch_100(b *testing.B) {
	pool := setup(b)
	eventID, _ := benchSeedEvent(b, pool, 300*b.N)
	// benchSeedEvent marks every unit allocated (the hold benchmark needs that); the batch starts with all of it free.
	_, err := pool.Exec(context.Background(), `UPDATE booking.event_supply SET allocated_units=0 WHERE event_id=$1::uuid`, eventID)
	require.NoError(b, err)
	repo := NewAdmissionRepo(pool)
	batches := make([][]outbound.ReserveEntry, b.N)
	for i := range batches {
		batch := make([]outbound.ReserveEntry, 0, 100)
		for j := 0; j < 100; j++ {
			batch = append(batch, outbound.ReserveEntry{
				QueueEntryID: newTestUUID(), Seq: int64(i*100 + j + 1), VisitorID: newTestUUID(), Units: 3, AdmissionJTI: newTestUUID(),
			})
		}
		batches[i] = batch
	}
	ctx := context.Background()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		out, err := repo.ReserveBatch(ctx, eventID, batches[i], time.Now())
		require.NoError(b, err)
		if out[len(out)-1].Outcome != "GRANTED" {
			b.Fatalf("batch %d: last entry %s, want GRANTED", i, out[len(out)-1].Outcome)
		}
	}
}
