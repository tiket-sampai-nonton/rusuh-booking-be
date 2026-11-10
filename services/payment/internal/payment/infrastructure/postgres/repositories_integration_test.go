//go:build integration

package postgres

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tiket-sampai-nonton/rusuh-booking-be/pkg/migrate"
	"github.com/tiket-sampai-nonton/rusuh-booking-be/pkg/telemetry"

	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/payment/deployments/migrations"
	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/payment/internal/payment/domain/entity"
	vo "github.com/tiket-sampai-nonton/rusuh-booking-be/services/payment/internal/payment/domain/valueobject"
	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/payment/internal/payment/port/outbound"
)

const (
	bookingID = "11111111-1111-1111-1111-111111111111"
	visitorID = "22222222-2222-2222-2222-222222222222"
)

// setup membuat schema it_payment baru dan menjalankan migrasi yang sama dengan `payment migrate up`.
func setup(t *testing.T) *pgxpool.Pool {
	t.Helper()
	dsn := os.Getenv("TEST_POSTGRES_DSN")
	if dsn == "" {
		t.Skip("TEST_POSTGRES_DSN tidak diset")
	}
	ctx := context.Background()
	admin, err := pgxpool.New(ctx, dsn)
	require.NoError(t, err)
	_, err = admin.Exec(ctx, `CREATE EXTENSION IF NOT EXISTS pgcrypto; DROP SCHEMA IF EXISTS it_payment CASCADE; CREATE SCHEMA it_payment`)
	require.NoError(t, err)
	admin.Close()

	cfg, err := pgxpool.ParseConfig(dsn)
	require.NoError(t, err)
	cfg.ConnConfig.RuntimeParams["search_path"] = "it_payment"
	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	require.NoError(t, err)
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), `DROP SCHEMA IF EXISTS it_payment CASCADE`)
		pool.Close()
	})
	migs, err := migrate.Load(migrations.FS, ".")
	require.NoError(t, err)
	r, err := migrate.NewRunner(pool, "it_payment", migs, slog.New(slog.NewTextHandler(io.Discard, nil)))
	require.NoError(t, err)
	_, err = r.Up(ctx, 0)
	require.NoError(t, err)
	return pool
}

func newAttempt() outbound.NewAttempt {
	return outbound.NewAttempt{
		BookingID: bookingID, PublicRef: "RB-7F3A9C21", Method: vo.MethodVABCA, AmountMinor: 1500000,
		ExpiresAt: time.Now().Add(10 * time.Minute), BookingVersion: 3,
	}
}

func outboxCount(t *testing.T, pool *pgxpool.Pool) int {
	t.Helper()
	var n int
	require.NoError(t, pool.QueryRow(context.Background(), `SELECT count(*) FROM outbox_events`).Scan(&n))
	return n
}

func succeeded(attemptID string, at time.Time) outbound.TransitionSpec {
	return outbound.TransitionSpec{
		AttemptID: attemptID, To: vo.AttemptSucceeded, ProviderTxID: "tx-1", SettlementTime: &at, LastProviderStatus: "settlement",
		Event: &outbound.Event{RoutingKey: "payment.succeeded", Payload: map[string]string{"bookingId": bookingID}},
	}
}

func TestCreateNext_NumbersAttemptsAndBuildsOrderID(t *testing.T) {
	ctx := context.Background()
	repo := NewAttempts(setup(t))

	a1, err := repo.CreateNext(ctx, newAttempt())
	require.NoError(t, err)
	assert.Equal(t, 1, a1.AttemptNo)
	assert.Equal(t, "RB-7F3A9C21-1", a1.ProviderOrderID)
	assert.Equal(t, vo.AttemptCreated, a1.Status)
	assert.Equal(t, 3, a1.BookingVersion)
	assert.EqualValues(t, 1500000, a1.AmountMinor)

	_, err = repo.CreateNext(ctx, newAttempt())
	require.ErrorIs(t, err, outbound.ErrOpenAttemptExists, "satu attempt terbuka per booking")

	ok, err := repo.Transition(ctx, outbound.TransitionSpec{AttemptID: a1.ID, To: vo.AttemptCancelled})
	require.NoError(t, err)
	require.True(t, ok)
	a2, err := repo.CreateNext(ctx, newAttempt())
	require.NoError(t, err)
	assert.Equal(t, 2, a2.AttemptNo)
	assert.Equal(t, "RB-7F3A9C21-2", a2.ProviderOrderID)
}

// Property 1: At most one open attempt — dua puluh permintaan bersamaan, satu pemenang.
func TestCreateNext_ConcurrentRequestsCreateOneOpenAttempt(t *testing.T) {
	ctx := context.Background()
	pool := setup(t)
	repo := NewAttempts(pool)

	const workers = 20
	var wins, conflicts atomic.Int32
	var wg sync.WaitGroup
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := repo.CreateNext(ctx, newAttempt())
			switch {
			case err == nil:
				wins.Add(1)
			case assert.ErrorIs(t, err, outbound.ErrOpenAttemptExists):
				conflicts.Add(1)
			}
		}()
	}
	wg.Wait()
	assert.EqualValues(t, 1, wins.Load())
	assert.EqualValues(t, workers-1, conflicts.Load())

	var open int
	require.NoError(t, pool.QueryRow(ctx, `SELECT count(*) FROM payment_attempts WHERE status IN ('CREATED','PENDING','UNKNOWN')`).Scan(&open))
	assert.Equal(t, 1, open)
}

func TestTransition_StoresInstructionAndWritesEventOnlyWhenApplied(t *testing.T) {
	ctx := context.Background()
	pool := setup(t)
	repo := NewAttempts(pool)
	a, err := repo.CreateNext(ctx, newAttempt())
	require.NoError(t, err)

	ins := &entity.Instruction{Type: "SNAP", SnapToken: "tok-1", RedirectURL: "https://snap/r/tok-1"}
	ok, err := repo.Transition(ctx, outbound.TransitionSpec{AttemptID: a.ID, To: vo.AttemptPending, ProviderTxID: "tx-1", Instruction: ins})
	require.NoError(t, err)
	require.True(t, ok)
	got, found, err := repo.FindByOrderID(ctx, "RB-7F3A9C21-1")
	require.NoError(t, err)
	require.True(t, found)
	assert.Equal(t, vo.AttemptPending, got.Status)
	assert.Equal(t, "tx-1", got.ProviderTxID)
	assert.Equal(t, ins, got.Instruction)
	assert.Equal(t, 0, outboxCount(t, pool), "transisi tanpa event tidak menulis outbox")

	at := time.Date(2026, 10, 26, 10, 30, 5, 0, time.UTC)
	ok, err = repo.Transition(ctx, succeeded(a.ID, at))
	require.NoError(t, err)
	assert.True(t, ok)
	assert.Equal(t, 1, outboxCount(t, pool), "uang berubah → event ditulis di transaksi yang sama")

	got, _, _ = repo.FindByOrderID(ctx, "RB-7F3A9C21-1")
	assert.Equal(t, vo.AttemptSucceeded, got.Status)
	require.NotNil(t, got.SettlementTime)
	assert.True(t, got.SettlementTime.Equal(at))
	assert.Equal(t, ins, got.Instruction, "instruksi lama tidak terhapus")

	var routing, aggregate string
	require.NoError(t, pool.QueryRow(ctx, `SELECT routing_key, aggregate_id::text FROM outbox_events`).Scan(&routing, &aggregate))
	assert.Equal(t, "payment.succeeded", routing)
	assert.Equal(t, bookingID, aggregate)
}

// Property 4: webhook idempoten dan status final tidak mundur.
func TestTransition_DuplicateAndOutOfOrderAreNoOps(t *testing.T) {
	ctx := context.Background()
	pool := setup(t)
	repo := NewAttempts(pool)
	a, err := repo.CreateNext(ctx, newAttempt())
	require.NoError(t, err)
	_, err = repo.Transition(ctx, outbound.TransitionSpec{AttemptID: a.ID, To: vo.AttemptPending})
	require.NoError(t, err)

	at := time.Now().UTC()
	ok, err := repo.Transition(ctx, succeeded(a.ID, at))
	require.NoError(t, err)
	require.True(t, ok)

	for name, to := range map[string]vo.AttemptStatus{
		"settlement yang sama": vo.AttemptSucceeded, "pending terlambat": vo.AttemptPending,
		"expire terlambat": vo.AttemptExpired, "cancel terlambat": vo.AttemptCancelled, "failed terlambat": vo.AttemptFailed,
	} {
		spec := outbound.TransitionSpec{AttemptID: a.ID, To: to}
		if to == vo.AttemptSucceeded {
			spec = succeeded(a.ID, at)
		}
		ok, err := repo.Transition(ctx, spec)
		require.NoError(t, err, name)
		assert.False(t, ok, name)
	}
	got, _, _ := repo.FindLatest(ctx, bookingID)
	assert.Equal(t, vo.AttemptSucceeded, got.Status)
	assert.Equal(t, 1, outboxCount(t, pool), "payment.succeeded ditulis paling banyak sekali per attempt")
}

func TestTransition_SettlementAfterCancelOrExpiryIsAllowedButNotAfterFailure(t *testing.T) {
	ctx := context.Background()
	pool := setup(t)
	repo := NewAttempts(pool)
	for _, late := range []vo.AttemptStatus{vo.AttemptCancelled, vo.AttemptExpired} {
		_, err := pool.Exec(ctx, `DELETE FROM outbox_events; DELETE FROM payment_attempts`)
		require.NoError(t, err)
		a, err := repo.CreateNext(ctx, newAttempt())
		require.NoError(t, err)
		_, err = repo.Transition(ctx, outbound.TransitionSpec{AttemptID: a.ID, To: vo.AttemptPending})
		require.NoError(t, err)
		_, err = repo.Transition(ctx, outbound.TransitionSpec{AttemptID: a.ID, To: late})
		require.NoError(t, err)
		ok, err := repo.Transition(ctx, succeeded(a.ID, time.Now()))
		require.NoError(t, err)
		assert.True(t, ok, "uang menang atas %s (design §4.1)", late)
	}

	_, err := pool.Exec(ctx, `DELETE FROM outbox_events; DELETE FROM payment_attempts`)
	require.NoError(t, err)
	a, _ := repo.CreateNext(ctx, newAttempt())
	_, _ = repo.Transition(ctx, outbound.TransitionSpec{AttemptID: a.ID, To: vo.AttemptFailed})
	ok, err := repo.Transition(ctx, succeeded(a.ID, time.Now()))
	require.NoError(t, err)
	assert.False(t, ok, "FAILED final")
}

// Sepuluh webhook settlement bersamaan: satu perubahan status dan satu baris outbox.
func TestTransition_ConcurrentDuplicatesApplyOnce(t *testing.T) {
	ctx := context.Background()
	pool := setup(t)
	repo := NewAttempts(pool)
	a, err := repo.CreateNext(ctx, newAttempt())
	require.NoError(t, err)
	_, err = repo.Transition(ctx, outbound.TransitionSpec{AttemptID: a.ID, To: vo.AttemptPending})
	require.NoError(t, err)

	var applied atomic.Int32
	var wg sync.WaitGroup
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			ok, err := repo.Transition(ctx, succeeded(a.ID, time.Now()))
			if assert.NoError(t, err) && ok {
				applied.Add(1)
			}
		}()
	}
	wg.Wait()
	assert.EqualValues(t, 1, applied.Load())
	assert.Equal(t, 1, outboxCount(t, pool))
}

func TestFindHelpers(t *testing.T) {
	ctx := context.Background()
	repo := NewAttempts(setup(t))

	_, found, err := repo.FindOpen(ctx, bookingID)
	require.NoError(t, err)
	assert.False(t, found)
	_, found, err = repo.FindLatest(ctx, "bukan-uuid")
	require.NoError(t, err)
	assert.False(t, found, "id tidak valid = tidak ditemukan")

	a1, _ := repo.CreateNext(ctx, newAttempt())
	_, _ = repo.Transition(ctx, outbound.TransitionSpec{AttemptID: a1.ID, To: vo.AttemptCancelled})
	a2, _ := repo.CreateNext(ctx, newAttempt())

	open, found, err := repo.FindOpen(ctx, bookingID)
	require.NoError(t, err)
	require.True(t, found)
	assert.Equal(t, a2.ID, open.ID)
	latest, _, _ := repo.FindLatest(ctx, bookingID)
	assert.Equal(t, 2, latest.AttemptNo)
	_, found, _ = repo.FindByOrderID(ctx, "RB-NOPE-1")
	assert.False(t, found)
}

func TestCallbacks_RecordKeepsDuplicateOnce(t *testing.T) {
	ctx := context.Background()
	repo := NewCallbacks(setup(t))
	amount := int64(1500000)
	cb := outbound.Callback{Provider: "MIDTRANS", Fingerprint: []byte("fp-1"), ProviderOrderID: "RB-1-1", NormalizedStatus: "SUCCEEDED",
		SignatureValid: true, AmountMinor: &amount, PayloadDigest: []byte("digest"), Result: outbound.CallbackProcessed}

	inserted, err := repo.Record(ctx, cb)
	require.NoError(t, err)
	assert.True(t, inserted)
	inserted, err = repo.Record(ctx, cb)
	require.NoError(t, err)
	assert.False(t, inserted, "callback yang sama tidak disimpan dua kali")

	cb.Fingerprint = []byte("fp-2") // progres status = fingerprint baru
	cb.SignatureValid, cb.Result, cb.AmountMinor = false, outbound.CallbackRejected, nil
	inserted, err = repo.Record(ctx, cb)
	require.NoError(t, err)
	assert.True(t, inserted)
}

func TestIdempotency_BeginCompleteReleaseAndExpiry(t *testing.T) {
	ctx := context.Background()
	pool := setup(t)
	s := NewIdempotency(pool)
	const key = "idem-key-0123456789"

	rec, found, err := s.Begin(ctx, "create_attempt", visitorID, key, []byte("h1"), time.Hour)
	require.NoError(t, err)
	assert.False(t, found)
	assert.Equal(t, "IN_PROGRESS", rec.State)

	rec, found, err = s.Begin(ctx, "create_attempt", visitorID, key, []byte("h1"), time.Hour)
	require.NoError(t, err)
	assert.True(t, found)
	assert.Equal(t, "IN_PROGRESS", rec.State, "permintaan kembar yang masih berjalan")

	require.NoError(t, s.Complete(ctx, "create_attempt", visitorID, key, 201, []byte(`{"attemptNo":1}`)))
	rec, found, err = s.Begin(ctx, "create_attempt", visitorID, key, []byte("h2"), time.Hour)
	require.NoError(t, err)
	assert.True(t, found)
	assert.Equal(t, "COMPLETED", rec.State)
	assert.Equal(t, []byte("h1"), rec.RequestHash, "hash asli dikembalikan agar key dengan payload beda terdeteksi")
	assert.JSONEq(t, `{"attemptNo":1}`, string(rec.ResponseBody))

	require.NoError(t, s.Release(ctx, "create_attempt", visitorID, key))
	_, found, _ = s.Begin(ctx, "create_attempt", visitorID, key, []byte("h1"), time.Hour)
	assert.True(t, found, "Release tidak menghapus record yang sudah COMPLETED")

	const key2 = "idem-key-failed-0001"
	_, _, _ = s.Begin(ctx, "create_attempt", visitorID, key2, []byte("h"), time.Hour)
	require.NoError(t, s.Release(ctx, "create_attempt", visitorID, key2))
	_, found, err = s.Begin(ctx, "create_attempt", visitorID, key2, []byte("h"), time.Hour)
	require.NoError(t, err)
	assert.False(t, found, "setelah gagal, key yang sama bisa dicoba lagi")

	_, err = pool.Exec(ctx, `UPDATE idempotency_records SET expires_at = now() - interval '1 second' WHERE idempotency_key = $1`, key)
	require.NoError(t, err)
	_, found, err = s.Begin(ctx, "create_attempt", visitorID, key, []byte("h3"), time.Hour)
	require.NoError(t, err)
	assert.False(t, found, "record kedaluwarsa diganti")
}

func TestPageTokens_OneTimeAndExpiry(t *testing.T) {
	ctx := context.Background()
	s := NewPageTokens(setup(t))
	now := time.Now().UTC()

	require.NoError(t, s.Create(ctx, []byte("hash-ok"), bookingID, visitorID, now.Add(time.Minute)))
	ps, err := s.Consume(ctx, []byte("hash-ok"), now)
	require.NoError(t, err)
	assert.Equal(t, outbound.PageSession{BookingID: bookingID, VisitorID: visitorID}, ps)

	_, err = s.Consume(ctx, []byte("hash-ok"), now)
	assert.ErrorIs(t, err, outbound.ErrPageTokenInvalid, "token dipakai ulang (replay) ditolak")

	require.NoError(t, s.Create(ctx, []byte("hash-old"), bookingID, visitorID, now.Add(time.Minute)))
	_, err = s.Consume(ctx, []byte("hash-old"), now.Add(2*time.Minute))
	assert.ErrorIs(t, err, outbound.ErrPageTokenInvalid, "kedaluwarsa")

	_, err = s.Consume(ctx, []byte("tidak-ada"), now)
	assert.ErrorIs(t, err, outbound.ErrPageTokenInvalid)

	// Konsumsi bersamaan: tepat satu yang berhasil.
	require.NoError(t, s.Create(ctx, []byte("hash-race"), bookingID, visitorID, now.Add(time.Minute)))
	var okCount atomic.Int32
	var wg sync.WaitGroup
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, err := s.Consume(ctx, []byte("hash-race"), now); err == nil {
				okCount.Add(1)
			}
		}()
	}
	wg.Wait()
	assert.EqualValues(t, 1, okCount.Load())
}

// scrape returns the value of one metric line from /metrics, 0 when the series does not exist yet.
func scrape(t *testing.T, series string) float64 {
	t.Helper()
	rec := httptest.NewRecorder()
	telemetry.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/metrics", nil))
	for _, line := range strings.Split(rec.Body.String(), "\n") {
		if strings.HasPrefix(line, series+" ") {
			v, err := strconv.ParseFloat(strings.TrimPrefix(line, series+" "), 64)
			require.NoError(t, err)
			return v
		}
	}
	return 0
}

// NFR-09: conversion and expiry rate come from these counters; applied transitions count once, no-ops never.
func TestMetrics_CountAttemptsByMethodAndStatusAndWebhooksByResult(t *testing.T) {
	ctx := context.Background()
	pool := setup(t)
	attempts := NewAttempts(pool)

	created := `rbt_payment_attempts_total{method="VA_BCA",status="CREATED"}`
	succeededSeries := `rbt_payment_attempts_total{method="VA_BCA",status="SUCCEEDED"}`
	processed := `rbt_payment_webhooks_total{result="PROCESSED"}`
	duplicate := `rbt_payment_webhooks_total{result="DUPLICATE"}`
	c0, s0, p0, d0 := scrape(t, created), scrape(t, succeededSeries), scrape(t, processed), scrape(t, duplicate)

	a, err := attempts.CreateNext(ctx, newAttempt())
	require.NoError(t, err)
	_, err = attempts.Transition(ctx, outbound.TransitionSpec{AttemptID: a.ID, To: vo.AttemptPending})
	require.NoError(t, err)
	ok, err := attempts.Transition(ctx, succeeded(a.ID, time.Now()))
	require.NoError(t, err)
	require.True(t, ok)
	ok, err = attempts.Transition(ctx, succeeded(a.ID, time.Now())) // duplicate: not counted
	require.NoError(t, err)
	require.False(t, ok)

	cb := outbound.Callback{Provider: "MIDTRANS", Fingerprint: []byte("m-1"), ProviderOrderID: "RB-1-1", NormalizedStatus: "SUCCEEDED",
		SignatureValid: true, PayloadDigest: []byte("d"), Result: outbound.CallbackProcessed}
	callbacks := NewCallbacks(pool)
	_, err = callbacks.Record(ctx, cb)
	require.NoError(t, err)
	_, err = callbacks.Record(ctx, cb)
	require.NoError(t, err)

	assert.Equal(t, c0+1, scrape(t, created))
	assert.Equal(t, s0+1, scrape(t, succeededSeries), "transisi ganda tidak dihitung dua kali")
	assert.Equal(t, p0+1, scrape(t, processed))
	assert.Equal(t, d0+1, scrape(t, duplicate))
}
