//go:build integration

package postgres_test

import (
	"context"
	"errors"
	"fmt"
	"os"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tiket-sampai-nonton/rusuh-booking-be/pkg/eventstore"
	pkgpg "github.com/tiket-sampai-nonton/rusuh-booking-be/pkg/postgres"
	domevent "github.com/tiket-sampai-nonton/rusuh-booking-be/services/booking/internal/booking/domain/event"
	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/booking/internal/booking/infrastructure/postgres"
	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/booking/internal/booking/port/outbound"
)

// testPool opens a pgx pool for integration tests. Uses TEST_POSTGRES_DSN or Makefile env vars.
func testPool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	dsn := os.Getenv("TEST_POSTGRES_DSN")
	if dsn == "" {
		dsn = fmt.Sprintf(
			"host=%s port=%s user=%s password=%s dbname=%s sslmode=disable search_path=booking",
			envOr("TEST_POSTGRES_HOST", "localhost"),
			envOr("TEST_POSTGRES_PORT", "5432"),
			envOr("TEST_POSTGRES_USER", "rbt"),
			envOr("TEST_POSTGRES_PASSWORD", "rbt-local-only"),
			envOr("TEST_POSTGRES_DATABASE", "rbt"),
		)
	}
	cfg, err := pgxpool.ParseConfig(dsn)
	require.NoError(t, err, "parse TEST_POSTGRES_DSN")
	cfg.MaxConns = 30
	cfg.ConnConfig.RuntimeParams["search_path"] = "booking" // the Makefile DSN has none
	pool, err := pgxpool.NewWithConfig(context.Background(), cfg)
	require.NoError(t, err, "pgxpool.New")
	t.Cleanup(pool.Close)
	return pool
}

func envOr(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

// seedTestEvent inserts a minimal event + 1 category + supply with the given capacity.
func seedTestEvent(t *testing.T, pool *pgxpool.Pool, suffix string, capacity int) (eventID, catID string) {
	t.Helper()
	ctx := context.Background()
	eventID = fmt.Sprintf("e2e00000-0000-0000-0000-%s", suffix)
	catID = fmt.Sprintf("e2e00000-0000-0001-0000-%s", suffix)

	_, err := pool.Exec(ctx, `
		INSERT INTO booking.events
			(id, slug, name, venue, starts_at, sales_starts_at, sales_ends_at, status,
			 max_tickets_per_order, max_tickets_per_buyer_nik, selection_ttl_seconds,
			 payment_ttl_seconds, claim_window_seconds, max_attendee_checks_per_session)
		VALUES ($1::uuid, $2, $2, 'Venue', now()+interval '1 hour', now()-interval '1 hour',
			now()+interval '2 hours', 'AVAILABLE', 1, 3, 300, 600, 180, 10)
		ON CONFLICT (id) DO NOTHING`, eventID, "slug-"+eventID)
	require.NoError(t, err)

	_, err = pool.Exec(ctx, `
		INSERT INTO booking.ticket_categories (id, event_id, code, name, price_minor, currency, display_order)
		VALUES ($1::uuid, $2::uuid, 'CAT', 'Category', 10000, 'IDR', 1)
		ON CONFLICT (id) DO NOTHING`, catID, eventID)
	require.NoError(t, err)

	_, err = pool.Exec(ctx, `
		INSERT INTO booking.category_inventories (category_id, event_id, capacity)
		VALUES ($1::uuid, $2::uuid, $3)
		ON CONFLICT (category_id) DO UPDATE SET capacity=$3`, catID, eventID, capacity)
	require.NoError(t, err)

	_, err = pool.Exec(ctx, `
		INSERT INTO booking.event_supply (event_id, total_capacity, allocated_units)
		VALUES ($1::uuid, $2, $2)
		ON CONFLICT (event_id) DO UPDATE SET total_capacity=$2, allocated_units=$2`, eventID, capacity)
	require.NoError(t, err)

	return eventID, catID
}

// seedActiveAdmission inserts an ACTIVE admission for use in a hold test goroutine.
func seedActiveAdmission(t *testing.T, pool *pgxpool.Pool, admID, eventID, visitorID string, nikHash []byte) {
	t.Helper()
	_, err := pool.Exec(context.Background(), `
		INSERT INTO booking.admissions
			(id, event_id, queue_entry_id, queue_seq, visitor_id,
			 buyer_nik_hash, buyer_email,
			 units, status, admission_jti,
			 cfg_selection_ttl_seconds, cfg_payment_ttl_seconds, cfg_submit_grace_seconds,
			 cfg_edit_enabled, cfg_max_edits, cfg_allow_mixed_categories,
			 reserved_at, expires_at)
		VALUES ($1::uuid, $2::uuid, gen_random_uuid(), 1, $3::uuid,
			$4, 'test@test.id',
			1, 'ACTIVE', gen_random_uuid(),
			300, 600, 5, true, 3, true,
			now(), now()+interval '10 minutes')
		ON CONFLICT (id) DO NOTHING`, admID, eventID, visitorID, nikHash)
	require.NoError(t, err)
}

// TestTC_BKG07_NoOversell (Property 1) — 100 concurrent holds on a 50-ticket event must yield
// exactly 50 successes and 50 CATEGORY_UNAVAILABLE errors. TC-BKG-07, design §14.
func TestTC_BKG07_NoOversell(t *testing.T) {
	const totalTickets = 50
	const goroutines = 100

	pool := testPool(t)
	suffix := fmt.Sprintf("%012d", time.Now().UnixNano()%1e12)
	eventID, catID := seedTestEvent(t, pool, suffix, totalTickets)

	uow, err := postgres.NewUnitOfWork(pool)
	require.NoError(t, err)

	var success, failed int64
	var wg sync.WaitGroup
	wg.Add(goroutines)

	for i := 0; i < goroutines; i++ {
		i := i
		go func() {
			defer wg.Done()
			ctx := context.Background()
			admID := fmt.Sprintf("ad000000-0000-0000-%04d-%s", i, suffix)
			visitorID := fmt.Sprintf("b1500000-0000-0000-%04d-%s", i, suffix)
			bookingID := fmt.Sprintf("b0000000-0000-0000-%04d-%s", i, suffix)
			nikHash := []byte(fmt.Sprintf("nik%08d", i))

			seedActiveAdmission(t, pool, admID, eventID, visitorID, nikHash)

			spec := outbound.HoldSpec{
				BookingID:        bookingID,
				PublicRef:        fmt.Sprintf("RB%08d", (time.Now().UnixNano()/1e3%1e6)*100+int64(i)), // unique across runs
				EventID:          eventID,
				AdmissionID:      admID,
				VisitorID:        visitorID,
				BuyerNIKHash:     nikHash,
				BuyerEmail:       fmt.Sprintf("u%d@t.id", i),
				AmountMinor:      10000,
				PaymentExpiresAt: time.Now().Add(10 * time.Minute),
				Lines: []outbound.HoldLine{{
					CategoryID:        catID,
					UnitPriceMinor:    10000,
					AttendeeNIKHash:   nikHash,
					AttendeeNIKEnc:    nikHash,
					AttendeeNIKMasked: "****",
					AttendeeName:      fmt.Sprintf("User %d", i),
				}},
				CorrelationID:  fmt.Sprintf("corr-%d", i),
				CfgEditEnabled: true,
				AdmissionUnits: 1,
			}
			if _, holdErr := uow.Hold(ctx, spec); holdErr == nil {
				atomic.AddInt64(&success, 1)
			} else {
				atomic.AddInt64(&failed, 1)
			}
		}()
	}
	wg.Wait()

	assert.Equal(t, int64(totalTickets), success,
		"exactly %d holds must succeed — no oversell", totalTickets)
	assert.Equal(t, int64(goroutines-totalTickets), failed,
		"remaining %d must fail with CATEGORY_UNAVAILABLE", goroutines-totalTickets)

	// Verify DB invariant: held_quantity == totalTickets.
	var held int
	require.NoError(t, pool.QueryRow(context.Background(),
		`SELECT held_quantity FROM booking.category_inventories WHERE category_id=$1::uuid`, catID).Scan(&held))
	assert.Equal(t, totalTickets, held, "held_quantity must equal exactly %d", totalTickets)

	// No oversell check: held + sold <= capacity.
	var cap, heldQ, soldQ int
	require.NoError(t, pool.QueryRow(context.Background(),
		`SELECT capacity, held_quantity, sold_quantity FROM booking.category_inventories WHERE category_id=$1::uuid`,
		catID).Scan(&cap, &heldQ, &soldQ))
	assert.LessOrEqual(t, heldQ+soldQ, cap, "invariant: held+sold <= capacity")
}

// TestEventSourcing_ConcurrencyGuard (Property 10) — 20 concurrent appends at version 0 on the
// same stream: exactly 1 wins, 19 get ErrConcurrency, stream has exactly 1 event.
func TestEventSourcing_ConcurrencyGuard(t *testing.T) {
	const goroutines = 20
	pool := testPool(t)

	suffix := fmt.Sprintf("%012d", time.Now().UnixNano()%1e12)
	bookingID := fmt.Sprintf("e5000000-0000-0000-0000-%s", suffix)

	store, err := eventstore.New("booking_events")
	require.NoError(t, err)

	var wins, concErrs int64
	var wg sync.WaitGroup
	wg.Add(goroutines)

	for i := 0; i < goroutines; i++ {
		go func() {
			defer wg.Done()
			ctx := context.Background()
			appendErr := pkgpg.WithTx(ctx, pool, pgx.TxOptions{IsoLevel: pgx.ReadCommitted}, func(tx pgx.Tx) error {
				_, err := store.Append(ctx, tx, bookingID, 0, eventstore.NewEvent{
					Type:          domevent.TypeBookingHeld,
					SchemaVersion: 1,
					Payload:       map[string]string{"bookingId": bookingID},
				})
				return err
			})
			if appendErr == nil {
				atomic.AddInt64(&wins, 1)
			} else if errors.Is(appendErr, eventstore.ErrConcurrency) {
				atomic.AddInt64(&concErrs, 1)
			}
		}()
	}
	wg.Wait()

	assert.Equal(t, int64(1), wins, "exactly 1 goroutine must win the append")
	assert.Equal(t, int64(goroutines-1), concErrs,
		"remaining %d must get ErrConcurrency", goroutines-1)

	// Verify exactly 1 event written.
	var count int
	require.NoError(t, pool.QueryRow(context.Background(),
		`SELECT COUNT(*) FROM booking.booking_events WHERE stream_id=$1::uuid`, bookingID).Scan(&count))
	assert.Equal(t, 1, count, "stream must have exactly 1 event")
}
