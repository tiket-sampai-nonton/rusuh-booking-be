//go:build integration

package postgres

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tiket-sampai-nonton/rusuh-booking-be/pkg/migrate"
	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/queue/deployments/migrations"
	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/queue/internal/queue/domain/entity"
	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/queue/internal/queue/port/outbound"
)

const adminEv = "11111111-1111-1111-1111-111111111111"

func intp(v int) *int    { return &v }
func boolp(v bool) *bool { return &v }

func apply(t *testing.T, repo *EventSettingsAdmin, patch entity.SettingsPatch, saleOpen bool, actor string, now time.Time) (outbound.ApplySettingsResult, error) {
	t.Helper()
	return repo.Apply(context.Background(), outbound.ApplySettingsParams{
		EventID: adminEv, Actor: actor, Now: now, Decide: entity.Decide(patch, saleOpen),
	})
}

func auditCount(t *testing.T, pool *pgxpool.Pool) int {
	t.Helper()
	var n int
	require.NoError(t, pool.QueryRow(context.Background(), `SELECT count(*) FROM event_settings_audit`).Scan(&n))
	return n
}

func TestEventSettingsAdmin_CreateWritesRowAndAllFieldsAudit(t *testing.T) {
	pool := setup(t)
	repo := NewEventSettingsAdmin(pool)
	now := time.Date(2026, 11, 6, 10, 0, 0, 0, time.UTC)

	res, err := apply(t, repo, entity.SettingsPatch{ActiveSlotCapacity: intp(77)}, false, "acct-1", now)
	require.NoError(t, err)
	assert.True(t, res.Created)
	assert.True(t, res.Changed)
	assert.Equal(t, adminEv, res.Settings.EventID)
	assert.Equal(t, 77, res.Settings.ActiveSlotCapacity)
	assert.Equal(t, 2, res.Settings.NearFrontMultiplier, "diabaikan field dipakai bawaan saat create")
	assert.True(t, res.Settings.UpdatedAt.Equal(now))

	var n, nilOld int
	require.NoError(t, pool.QueryRow(context.Background(),
		`SELECT count(*), count(*) FILTER (WHERE old_value IS NULL) FROM event_settings_audit WHERE event_id=$1::uuid AND actor='acct-1' AND changed_at=$2`,
		adminEv, now).Scan(&n, &nilOld))
	assert.Equal(t, 12, n)
	assert.Equal(t, 12, nilOld)
}

func TestEventSettingsAdmin_UpdateAuditsOnlyChangedFields(t *testing.T) {
	pool := setup(t)
	repo := NewEventSettingsAdmin(pool)
	t0 := time.Date(2026, 11, 6, 10, 0, 0, 0, time.UTC)
	_, err := apply(t, repo, entity.SettingsPatch{ActiveSlotCapacity: intp(77)}, false, "a", t0)
	require.NoError(t, err)

	t1 := t0.Add(time.Hour)
	res, err := apply(t, repo, entity.SettingsPatch{ActiveSlotCapacity: intp(90), PollNearMs: intp(2500)}, false, "b", t1)
	require.NoError(t, err)
	assert.False(t, res.Created)
	assert.True(t, res.Changed)
	assert.Equal(t, 2500, res.Settings.PollNearMs)

	rows, err := pool.Query(context.Background(),
		`SELECT field, old_value, new_value FROM event_settings_audit WHERE actor='b' ORDER BY id`)
	require.NoError(t, err)
	defer rows.Close()
	type r struct {
		f, o, n string
	}
	var got []r
	for rows.Next() {
		var x r
		require.NoError(t, rows.Scan(&x.f, &x.o, &x.n))
		got = append(got, x)
	}
	assert.Equal(t, []r{{"activeSlotCapacity", "77", "90"}, {"pollNearMs", "3000", "2500"}}, got)
}

func TestEventSettingsAdmin_IdenticalRepeatIsNoOp(t *testing.T) {
	pool := setup(t)
	repo := NewEventSettingsAdmin(pool)
	t0 := time.Date(2026, 11, 6, 10, 0, 0, 0, time.UTC)
	_, err := apply(t, repo, entity.SettingsPatch{ActiveSlotCapacity: intp(77)}, false, "a", t0)
	require.NoError(t, err)
	before := auditCount(t, pool)

	res, err := apply(t, repo, entity.SettingsPatch{ActiveSlotCapacity: intp(77)}, false, "b", t0.Add(time.Hour))
	require.NoError(t, err)
	assert.False(t, res.Created)
	assert.False(t, res.Changed)
	assert.Equal(t, before, auditCount(t, pool), "tanpa audit untuk request identik")
	assert.True(t, res.Settings.UpdatedAt.Equal(t0), "updated_at tidak berubah")
	var up time.Time
	require.NoError(t, pool.QueryRow(context.Background(), `SELECT updated_at FROM event_settings`).Scan(&up))
	assert.True(t, up.Equal(t0))
}

func TestEventSettingsAdmin_DecideErrorWritesNothing(t *testing.T) {
	pool := setup(t)
	repo := NewEventSettingsAdmin(pool)
	t0 := time.Date(2026, 11, 6, 10, 0, 0, 0, time.UTC)

	_, err := apply(t, repo, entity.SettingsPatch{ActiveSlotCapacity: intp(0)}, false, "a", t0)
	require.Error(t, err)
	var n int
	require.NoError(t, pool.QueryRow(context.Background(), `SELECT count(*) FROM event_settings`).Scan(&n))
	assert.Zero(t, n)
	assert.Zero(t, auditCount(t, pool))

	_, err = apply(t, repo, entity.SettingsPatch{ActiveSlotCapacity: intp(5), OneActivePerEmail: boolp(false)}, true, "a", t0)
	assert.ErrorIs(t, err, entity.ErrRulesLocked, "create saat sale terbuka dengan nilai terkunci bukan bawaan")
	require.NoError(t, pool.QueryRow(context.Background(), `SELECT count(*) FROM event_settings`).Scan(&n))
	assert.Zero(t, n)

	_, err = apply(t, repo, entity.SettingsPatch{ActiveSlotCapacity: intp(5)}, true, "a", t0)
	require.NoError(t, err, "create saat sale terbuka dengan nilai terkunci bawaan diizinkan")
	before := auditCount(t, pool)
	_, err = apply(t, repo, entity.SettingsPatch{ActiveSlotCapacity: intp(6), OneActivePerVisitor: boolp(false)}, true, "a", t0)
	assert.ErrorIs(t, err, entity.ErrRulesLocked)
	assert.Equal(t, before, auditCount(t, pool))
	var capacity int
	require.NoError(t, pool.QueryRow(context.Background(), `SELECT active_slot_capacity FROM event_settings`).Scan(&capacity))
	assert.Equal(t, 5, capacity)
}

func TestEventSettingsAdmin_AuditFailureRollsBackRow(t *testing.T) {
	pool := setup(t)
	repo := NewEventSettingsAdmin(pool)
	ctx := context.Background()
	t0 := time.Date(2026, 11, 6, 10, 0, 0, 0, time.UTC)
	_, err := apply(t, repo, entity.SettingsPatch{ActiveSlotCapacity: intp(77)}, false, "a", t0)
	require.NoError(t, err)

	_, err = pool.Exec(ctx, `CREATE FUNCTION fail_audit() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'boom'; END $$`)
	require.NoError(t, err)
	_, err = pool.Exec(ctx, `CREATE TRIGGER fail_audit_t BEFORE INSERT ON event_settings_audit FOR EACH ROW WHEN (NEW.field = 'pollNearMs') EXECUTE FUNCTION fail_audit()`)
	require.NoError(t, err)

	var capBefore int
	var upBefore time.Time
	require.NoError(t, pool.QueryRow(ctx, `SELECT active_slot_capacity, updated_at FROM event_settings`).Scan(&capBefore, &upBefore))
	auditBefore := auditCount(t, pool)

	_, err = apply(t, repo, entity.SettingsPatch{ActiveSlotCapacity: intp(99), PollNearMs: intp(2500)}, false, "b", t0.Add(time.Hour))
	require.Error(t, err)

	var capAfter int
	var upAfter time.Time
	require.NoError(t, pool.QueryRow(ctx, `SELECT active_slot_capacity, updated_at FROM event_settings`).Scan(&capAfter, &upAfter))
	assert.Equal(t, capBefore, capAfter, "baris tidak berubah saat audit gagal")
	assert.True(t, upBefore.Equal(upAfter))
	assert.Equal(t, auditBefore, auditCount(t, pool))
}

func TestEventSettingsAdmin_AuditTableIsAppendOnly(t *testing.T) {
	pool := setup(t)
	repo := NewEventSettingsAdmin(pool)
	_, err := apply(t, repo, entity.SettingsPatch{ActiveSlotCapacity: intp(77)}, false, "a", time.Now())
	require.NoError(t, err)
	_, err = pool.Exec(context.Background(), `UPDATE event_settings_audit SET actor='x'`)
	assert.Error(t, err)
	_, err = pool.Exec(context.Background(), `DELETE FROM event_settings_audit`)
	assert.Error(t, err)
}

func TestEventSettingsAdmin_ConcurrentPutsAreSerialized(t *testing.T) {
	pool := setup(t)
	repo := NewEventSettingsAdmin(pool)
	const n = 12
	var wg sync.WaitGroup
	created := make(chan bool, n)
	errs := make(chan error, n)
	base := time.Date(2026, 11, 6, 10, 0, 0, 0, time.UTC)
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			res, err := apply(t, repo, entity.SettingsPatch{ActiveSlotCapacity: intp(100 + i)}, false, fmt.Sprintf("g%d", i), base.Add(time.Duration(i)*time.Second))
			if err != nil {
				errs <- err
				return
			}
			created <- res.Created
		}(i)
	}
	wg.Wait()
	close(created)
	close(errs)
	for err := range errs {
		require.NoError(t, err)
	}
	c := 0
	for v := range created {
		if v {
			c++
		}
	}
	assert.Equal(t, 1, c, "tepat satu request membuat baris")

	rows, err := pool.Query(context.Background(),
		`SELECT old_value, new_value FROM event_settings_audit WHERE field='activeSlotCapacity' ORDER BY id`)
	require.NoError(t, err)
	defer rows.Close()
	var prev *string
	k := 0
	for rows.Next() {
		var o *string
		var nv string
		require.NoError(t, rows.Scan(&o, &nv))
		if k == 0 {
			assert.Nil(t, o)
		} else {
			require.NotNil(t, o)
			require.NotNil(t, prev)
			assert.Equal(t, *prev, *o, "rantai audit konsisten")
		}
		prev = &nv
		k++
	}
	assert.GreaterOrEqual(t, k, 1)
	var final int
	require.NoError(t, pool.QueryRow(context.Background(), `SELECT active_slot_capacity FROM event_settings`).Scan(&final))
	require.NotNil(t, prev)
	assert.Equal(t, fmt.Sprint(final), *prev, "nilai akhir = audit terakhir")
}

func TestDefaultSettings_MatchColumnDefaults(t *testing.T) {
	pool := setup(t)
	ctx := context.Background()
	_, err := pool.Exec(ctx, `INSERT INTO event_settings (event_id) VALUES ($1::uuid)`, adminEv)
	require.NoError(t, err)
	got, found, err := NewEventSettings(pool).Get(ctx, adminEv)
	require.NoError(t, err)
	require.True(t, found)
	want := entity.DefaultSettings(adminEv)
	got.UpdatedAt = time.Time{}
	assert.Equal(t, want, got)
}

func TestEventSettingsAdmin_InvalidEventID(t *testing.T) {
	pool := setup(t)
	repo := NewEventSettingsAdmin(pool)
	_, err := repo.Apply(context.Background(), outbound.ApplySettingsParams{
		EventID: "bukan-uuid", Actor: "a", Now: time.Now(), Decide: entity.Decide(entity.SettingsPatch{ActiveSlotCapacity: intp(5)}, false),
	})
	assert.Error(t, err)
}

// QSA-8.2: migration 0004 reverts and re-applies cleanly, and 0001-0003 are untouched by the round trip.
func TestMigration0004_DownThenUpAgain(t *testing.T) {
	pool := setup(t)
	ctx := context.Background()
	migs, err := migrate.Load(migrations.FS, ".")
	require.NoError(t, err)
	runner, err := migrate.NewRunner(pool, "queue", migs, slog.New(slog.NewTextHandler(io.Discard, nil)))
	require.NoError(t, err)

	exists := func() bool {
		var n int
		require.NoError(t, pool.QueryRow(ctx, `SELECT count(*) FROM information_schema.tables WHERE table_schema='queue' AND table_name='event_settings_audit'`).Scan(&n))
		return n == 1
	}
	require.True(t, exists())
	n, err := runner.Down(ctx, 2)
	require.NoError(t, err)
	assert.Equal(t, 2, n)
	assert.False(t, exists(), "down menghapus tabel audit")
	var fn int
	require.NoError(t, pool.QueryRow(ctx, `SELECT count(*) FROM pg_proc WHERE proname='event_settings_audit_immutable'`).Scan(&fn))
	assert.Zero(t, fn, "down menghapus fungsi trigger")
	var rows int
	require.NoError(t, pool.QueryRow(ctx, `SELECT count(*) FROM event_settings`).Scan(&rows), "tabel 0001 tetap ada")

	n, err = runner.Up(ctx, 0)
	require.NoError(t, err)
	assert.Equal(t, 2, n)
	assert.True(t, exists(), "up kedua membuat ulang tabel audit")
}
