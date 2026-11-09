//go:build integration

package delivery

import (
	"context"
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	paymentpg "github.com/tiket-sampai-nonton/rusuh-booking-be/services/payment/internal/payment/infrastructure/postgres"
	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/payment/internal/payment/port/outbound"
)

// openAttempt creates a PENDING attempt for bookingA through the public API and returns its id.
func (j *journey) openAttempt(idem string) string {
	j.t.Helper()
	hdr := map[string]string{"X-Visitor-Token": j.visitor, "Idempotency-Key": idem}
	code, _, raw := j.call(http.MethodPost, "/api/v1/payments/"+bookingA+"/attempts", `{"method":"VA_BCA"}`, hdr)
	require.Equal(j.t, 201, code, string(raw))
	var id string
	require.NoError(j.t, j.pool.QueryRow(context.Background(),
		`SELECT id::text FROM payment_attempts WHERE booking_id = $1::uuid ORDER BY attempt_no DESC LIMIT 1`, bookingA).Scan(&id))
	return id
}

func (j *journey) simulate(attemptID, scenario string) (int, []byte) {
	j.t.Helper()
	code, _, raw := j.call(http.MethodPost, "/api/v1/local-simulator/payments/"+attemptID+"/scenario",
		`{"scenario":"`+scenario+`"}`, j.asVisitor())
	return code, raw
}

func (j *journey) status(attemptID string) string {
	j.t.Helper()
	var st string
	require.NoError(j.t, j.pool.QueryRow(context.Background(), `SELECT status FROM payment_attempts WHERE id = $1::uuid`, attemptID).Scan(&st))
	return st
}

// P-9 (invariant 5): SETTLE, DUPLICATE_WEBHOOK and OUT_OF_ORDER all go through the real webhook usecase and end
// with one SUCCEEDED attempt and exactly one payment.succeeded event.
func TestSimulator_SuccessScenariosEmitOneSucceededEvent(t *testing.T) {
	for _, scenario := range []string{"SETTLE", "DUPLICATE_WEBHOOK", "OUT_OF_ORDER"} {
		t.Run(scenario, func(t *testing.T) {
			j := setupJourney(t)
			id := j.openAttempt("idem-sim-" + scenario + "-0001")

			code, raw := j.simulate(id, scenario)
			require.Equal(t, 202, code, string(raw))
			body := data[map[string]any](t, raw)
			assert.Equal(t, true, body["simulated"])
			assert.Equal(t, scenario, body["scenario"])

			assert.Equal(t, "SUCCEEDED", j.status(id))
			assert.Equal(t, 1, j.count(`SELECT count(*) FROM outbox_events WHERE routing_key = 'payment.succeeded'`))
			// Replaying the same scenario changes nothing more.
			code, _ = j.simulate(id, scenario)
			require.Equal(t, 202, code)
			assert.Equal(t, 1, j.count(`SELECT count(*) FROM outbox_events WHERE routing_key = 'payment.succeeded'`))
			assert.Equal(t, 0, j.count(`SELECT count(*) FROM outbox_events WHERE routing_key = 'payment.failed'`))
		})
	}
}

// P-11: DENY fails the attempt, emits payment.failed once, and the buyer can open a new attempt.
func TestSimulator_DenyFailsAttemptAndAllowsRetry(t *testing.T) {
	j := setupJourney(t)
	id := j.openAttempt("idem-sim-deny-00000001")

	code, raw := j.simulate(id, "DENY")
	require.Equal(t, 202, code, string(raw))
	assert.Equal(t, "FAILED", j.status(id))
	assert.Equal(t, 1, j.count(`SELECT count(*) FROM outbox_events WHERE routing_key = 'payment.failed'`))
	assert.Equal(t, 0, j.count(`SELECT count(*) FROM outbox_events WHERE routing_key = 'payment.succeeded'`))

	next := j.openAttempt("idem-sim-deny-00000002")
	assert.NotEqual(t, id, next)
	assert.Equal(t, "PENDING", j.status(next))
}

// P-10 (Payment side, DEV-1): EXPIRE then SETTLE_LATE. The attempt goes EXPIRED and is then SUCCEEDED by a settlement
// whose settlement_time is inside the deadline: one payment.succeeded; Booking decides PAID/REVIEW by its own state.
func TestSimulator_ExpireThenSettleLate(t *testing.T) {
	j := setupJourney(t)
	id := j.openAttempt("idem-sim-late-00000001")

	code, raw := j.simulate(id, "EXPIRE")
	require.Equal(t, 202, code, string(raw))
	assert.Equal(t, "EXPIRED", j.status(id))
	assert.Equal(t, 0, j.count(`SELECT count(*) FROM outbox_events WHERE routing_key = 'payment.succeeded'`))

	code, raw = j.simulate(id, "SETTLE_LATE")
	require.Equal(t, 202, code, string(raw))
	assert.Equal(t, "SUCCEEDED", j.status(id))
	assert.Equal(t, 1, j.count(`SELECT count(*) FROM outbox_events WHERE routing_key = 'payment.succeeded'`))

	var settled, expires time.Time
	require.NoError(t, j.pool.QueryRow(context.Background(),
		`SELECT settlement_time, expires_at FROM payment_attempts WHERE id = $1::uuid`, id).Scan(&settled, &expires))
	assert.True(t, settled.Before(expires), "settlement_time is before the attempt deadline (timeliness by settlement time)")
}

// Known limit (design F6): a settlement on a CREATED attempt is a no-op; the call still answers 202.
func TestSimulator_SettleOnCreatedAttemptIsNoOp(t *testing.T) {
	j := setupJourney(t)
	a, err := paymentpg.NewAttempts(j.pool).CreateNext(context.Background(), outbound.NewAttempt{
		BookingID: bookingB, PublicRef: "RB-0B0B0B0B", Method: "VA_BCA", AmountMinor: 1500000,
		ExpiresAt: time.Now().Add(8 * time.Minute), BookingVersion: 3,
	})
	require.NoError(t, err)

	code, raw := j.simulate(a.ID, "SETTLE")
	require.Equal(t, 202, code, string(raw))
	assert.Equal(t, "CREATED", j.status(a.ID))
	assert.Equal(t, 0, j.count(`SELECT count(*) FROM outbox_events`))
}

func TestSimulator_Rejections(t *testing.T) {
	j := setupJourney(t)
	id := j.openAttempt("idem-sim-rej-000000001")

	code, _, _ := j.call(http.MethodPost, "/api/v1/local-simulator/payments/"+id+"/scenario", `{"scenario":"SETTLE"}`, nil)
	assert.Equal(t, 401, code, "no token")
	code, _ = j.simulate(id, "REFUND")
	assert.Equal(t, 400, code, "unknown scenario")
	assert.Equal(t, "PENDING", j.status(id), "bad scenario changes nothing")
	code, raw := j.simulate("00000000-0000-0000-0000-000000000000", "SETTLE")
	assert.Equal(t, 404, code, string(raw))
	code, _ = j.simulate("bukan-uuid", "SETTLE")
	assert.Equal(t, 404, code)
}

// P-14: in local and ci the route exists; in dev, staging and prod it does not, and the answer is byte-equal to
// any unknown path (no existence leak).
func TestSimulator_RouteExistsOnlyInLocalAndCI(t *testing.T) {
	for env, want := range map[string]bool{"local": true, "ci": true, "dev": false, "staging": false, "prod": false} {
		t.Run(env, func(t *testing.T) {
			j := setupJourneyEnv(t, env)
			path := "/api/v1/local-simulator/payments/00000000-0000-0000-0000-000000000000/scenario"
			code, _, raw := j.call(http.MethodPost, path, `{"scenario":"SETTLE"}`, j.asVisitor())
			unknownCode, _, unknownRaw := j.call(http.MethodPost, "/api/v1/tidak-ada/sama-sekali", `{"scenario":"SETTLE"}`, j.asVisitor())
			assert.Equal(t, 404, code)
			if want {
				assert.Contains(t, string(raw), "NOT_FOUND", "registered: an unknown attempt is NOT_FOUND")
				return
			}
			assert.Equal(t, unknownCode, code)
			assert.Equal(t, string(unknownRaw), string(raw), "disabled route is indistinguishable from an unknown path")
		})
	}
}
