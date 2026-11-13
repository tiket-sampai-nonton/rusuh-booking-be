//go:build integration

package postgres

import (
	"context"
	"io"
	"log/slog"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tiket-sampai-nonton/rusuh-booking-be/pkg/migrate"

	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/user/deployments/migrations"
	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/user/internal/user/domain/entity"
)

// setup membuat schema users baru dan menjalankan migrasi yang sama dengan `user migrate up`.
func setup(t *testing.T) *pgxpool.Pool {
	t.Helper()
	dsn := os.Getenv("TEST_POSTGRES_DSN")
	if dsn == "" {
		t.Skip("TEST_POSTGRES_DSN tidak diset")
	}
	ctx := context.Background()
	admin, err := pgxpool.New(ctx, dsn)
	require.NoError(t, err)
	_, err = admin.Exec(ctx, `CREATE EXTENSION IF NOT EXISTS pgcrypto; DROP SCHEMA IF EXISTS it_users CASCADE; CREATE SCHEMA it_users`)
	require.NoError(t, err)
	admin.Close()

	cfg, err := pgxpool.ParseConfig(dsn)
	require.NoError(t, err)
	cfg.ConnConfig.RuntimeParams["search_path"] = "it_users"
	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	require.NoError(t, err)
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), `DROP SCHEMA IF EXISTS it_users CASCADE`)
		pool.Close()
	})
	migs, err := migrate.Load(migrations.FS, ".")
	require.NoError(t, err)
	r, err := migrate.NewRunner(pool, "it_users", migs, slog.New(slog.NewTextHandler(io.Discard, nil)))
	require.NoError(t, err)
	_, err = r.Up(ctx, 0)
	require.NoError(t, err)
	return pool
}

func TestAccounts(t *testing.T) {
	ctx := context.Background()
	pool := setup(t)
	repo := NewAccounts(pool)

	_, found, err := repo.FindByEmailHash(ctx, []byte("h1"))
	require.NoError(t, err)
	assert.False(t, found)

	a, err := repo.Create(ctx, entity.NewAccount{EmailHash: []byte("h1"), EmailEnc: []byte("enc"), EmailMasked: "ar***@gmail.com"})
	require.NoError(t, err)
	assert.Equal(t, entity.AccountActive, a.Status)
	again, err := repo.Create(ctx, entity.NewAccount{EmailHash: []byte("h1"), EmailEnc: []byte("enc2"), EmailMasked: "x"})
	require.NoError(t, err)
	assert.Equal(t, a.ID, again.ID, "login bersamaan untuk email sama tidak membuat akun ganda")

	got, found, err := repo.FindByEmailHash(ctx, []byte("h1"))
	require.NoError(t, err)
	assert.True(t, found)
	assert.Equal(t, a.ID, got.ID)

	name := "Arif"
	upd, err := repo.UpdateProfile(ctx, a.ID, entity.ProfileUpdate{FullName: &name, NIKHash: []byte("n1"), NIKEnc: []byte("e"), NIKMasked: "3174********0001"})
	require.NoError(t, err)
	assert.Equal(t, "Arif", upd.FullName)
	assert.True(t, upd.HasNIK())

	keepName, err := repo.UpdateProfile(ctx, a.ID, entity.ProfileUpdate{NIKHash: []byte("n2"), NIKEnc: []byte("e"), NIKMasked: "3174********0002"})
	require.NoError(t, err)
	assert.Equal(t, "Arif", keepName.FullName, "field nil tidak diubah")

	b, err := repo.Create(ctx, entity.NewAccount{EmailHash: []byte("h2"), EmailEnc: []byte("enc"), EmailMasked: "b***@x.id"})
	require.NoError(t, err)
	_, err = repo.UpdateProfile(ctx, b.ID, entity.ProfileUpdate{NIKHash: []byte("n2"), NIKEnc: []byte("e"), NIKMasked: "m"})
	assert.ErrorIs(t, err, entity.ErrNIKTaken)

	require.NoError(t, repo.TouchLogin(ctx, a.ID, time.Now()))
	got, err = repo.GetByID(ctx, a.ID)
	require.NoError(t, err)
	assert.NotNil(t, got.LastLoginAt)

	_, err = repo.GetByID(ctx, "bukan-uuid")
	assert.ErrorIs(t, err, entity.ErrNotFound)
	_, err = repo.GetByID(ctx, "6f1c0e7a-0000-4000-8000-000000000009")
	assert.ErrorIs(t, err, entity.ErrNotFound)
	_, err = repo.UpdateProfile(ctx, "6f1c0e7a-0000-4000-8000-000000000009", entity.ProfileUpdate{FullName: &name})
	assert.ErrorIs(t, err, entity.ErrNotFound)

	// Task 7.1: GetNIKHashByID — account without NIK returns nil, nil
	nikHashNil, err := repo.GetNIKHashByID(ctx, b.ID)
	require.NoError(t, err)
	assert.Nil(t, nikHashNil, "account without NIK must return nil hash")

	// Task 7.1: GetNIKHashByID — account with NIK returns stored hash
	nikHash, err := repo.GetNIKHashByID(ctx, a.ID)
	require.NoError(t, err)
	assert.Equal(t, []byte("n2"), nikHash, "account with NIK must return stored hash")

	// Task 7.1: GetEmailEncByID — returns stored encrypted email
	emailEnc, err := repo.GetEmailEncByID(ctx, a.ID)
	require.NoError(t, err)
	assert.Equal(t, []byte("enc"), emailEnc, "GetEmailEncByID must return the stored email_enc")

	// Task 7.1: GetNIKHashByID — unknown ID returns ErrNotFound
	_, err = repo.GetNIKHashByID(ctx, "6f1c0e7a-0000-4000-8000-000000000009")
	assert.ErrorIs(t, err, entity.ErrNotFound)

	// Task 7.1: GetEmailEncByID — unknown ID returns ErrNotFound
	_, err = repo.GetEmailEncByID(ctx, "6f1c0e7a-0000-4000-8000-000000000009")
	assert.ErrorIs(t, err, entity.ErrNotFound)

	// GetNIKEncByID — account without NIK returns nil, nil
	nikEncNil, err := repo.GetNIKEncByID(ctx, b.ID)
	require.NoError(t, err)
	assert.Nil(t, nikEncNil, "account without NIK must return nil nik_enc")

	// GetNIKEncByID — account with NIK returns stored ciphertext
	nikEnc, err := repo.GetNIKEncByID(ctx, a.ID)
	require.NoError(t, err)
	assert.Equal(t, []byte("e"), nikEnc, "GetNIKEncByID must return the stored nik_enc")

	// GetNIKEncByID — unknown ID returns ErrNotFound
	_, err = repo.GetNIKEncByID(ctx, "6f1c0e7a-0000-4000-8000-000000000009")
	assert.ErrorIs(t, err, entity.ErrNotFound)

	audit := NewAudit(pool)
	require.NoError(t, audit.Record(ctx, entity.LoginEvent{AccountID: a.ID, EmailHash: []byte("h1"), Outcome: entity.AuditLoginOK, ClientIP: "203.0.113.9"}))
	require.NoError(t, audit.Record(ctx, entity.LoginEvent{Outcome: entity.AuditRefreshReused}))
}

func TestSessions_RotateIsSingleWinner(t *testing.T) {
	ctx := context.Background()
	pool := setup(t)
	acc, err := NewAccounts(pool).Create(ctx, entity.NewAccount{EmailHash: []byte("h"), EmailEnc: []byte("e"), EmailMasked: "m"})
	require.NoError(t, err)
	repo := NewSessions(pool)
	exp := time.Now().Add(time.Hour)

	s, err := repo.Create(ctx, entity.NewSession{AccountID: acc.ID, RefreshTokenHash: []byte("r0"), ExpiresAt: exp})
	require.NoError(t, err)
	found, ok, err := repo.FindByTokenHash(ctx, []byte("r0"))
	require.NoError(t, err)
	assert.True(t, ok)
	assert.Equal(t, s.ID, found.ID)

	var wg sync.WaitGroup
	results := make([]error, 5)
	for i := 0; i < 5; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			_, results[i] = repo.Rotate(ctx, s.ID, entity.NewSession{AccountID: acc.ID, RefreshTokenHash: []byte{byte('a' + i)}, ExpiresAt: exp}, time.Now())
		}(i)
	}
	wg.Wait()
	wins := 0
	for _, e := range results {
		if e == nil {
			wins++
		} else {
			assert.ErrorIs(t, e, entity.ErrRevoked)
		}
	}
	assert.Equal(t, 1, wins)

	old, _, _ := repo.FindByTokenHash(ctx, []byte("r0"))
	assert.True(t, old.Revoked())

	_, err = repo.RevokeAllForAccount(ctx, acc.ID, time.Now(), time.Now().Add(-time.Hour))
	require.NoError(t, err)
	var active int
	require.NoError(t, pool.QueryRow(ctx, `SELECT count(*) FROM sessions WHERE revoked_at IS NULL`).Scan(&active))
	assert.Equal(t, 0, active)
	require.NoError(t, repo.Revoke(ctx, s.ID, time.Now()))
	_, ok, err = repo.FindByTokenHash(ctx, []byte("nope"))
	require.NoError(t, err)
	assert.False(t, ok)
}

// D-U1: replaced_by is set by rotation only; logout leaves it NULL.
func TestSessions_ReplacedByTracksRotationOnly(t *testing.T) {
	ctx := context.Background()
	pool := setup(t)
	acc, err := NewAccounts(pool).Create(ctx, entity.NewAccount{EmailHash: []byte("h"), EmailEnc: []byte("e"), EmailMasked: "m"})
	require.NoError(t, err)
	repo := NewSessions(pool)
	exp := time.Now().Add(time.Hour)

	old, err := repo.Create(ctx, entity.NewSession{AccountID: acc.ID, RefreshTokenHash: []byte("old"), ExpiresAt: exp})
	require.NoError(t, err)
	assert.Nil(t, old.ReplacedBy)

	next, err := repo.Rotate(ctx, old.ID, entity.NewSession{AccountID: acc.ID, RefreshTokenHash: []byte("new"), ExpiresAt: exp}, time.Now())
	require.NoError(t, err)
	rotated, ok, err := repo.FindByTokenHash(ctx, []byte("old"))
	require.NoError(t, err)
	require.True(t, ok)
	require.NotNil(t, rotated.ReplacedBy)
	assert.Equal(t, next.ID, *rotated.ReplacedBy)
	assert.True(t, rotated.Rotated())

	loggedOut, err := repo.Create(ctx, entity.NewSession{AccountID: acc.ID, RefreshTokenHash: []byte("out"), ExpiresAt: exp})
	require.NoError(t, err)
	require.NoError(t, repo.Revoke(ctx, loggedOut.ID, time.Now()))
	out, ok, err := repo.FindByTokenHash(ctx, []byte("out"))
	require.NoError(t, err)
	require.True(t, ok)
	assert.True(t, out.Revoked())
	assert.Nil(t, out.ReplacedBy, "logout tidak mengisi replaced_by")
	assert.False(t, out.Rotated())
}

func TestSessions_CreateWithPregeneratedID(t *testing.T) {
	ctx := context.Background()
	pool := setup(t)
	acc, err := NewAccounts(pool).Create(ctx, entity.NewAccount{EmailHash: []byte("h"), EmailEnc: []byte("e"), EmailMasked: "m"})
	require.NoError(t, err)
	repo := NewSessions(pool)
	exp := time.Now().Add(time.Hour)

	gen, err := repo.Create(ctx, entity.NewSession{AccountID: acc.ID, RefreshTokenHash: []byte("g"), ExpiresAt: exp})
	require.NoError(t, err)
	assert.NotEmpty(t, gen.ID, "ID kosong dibuat database")

	const given = "6f1c0e7a-0000-4000-8000-0000000000a1"
	s, err := repo.Create(ctx, entity.NewSession{ID: given, AccountID: acc.ID, RefreshTokenHash: []byte("p"), ExpiresAt: exp})
	require.NoError(t, err)
	assert.Equal(t, given, s.ID)

	const given2 = "6f1c0e7a-0000-4000-8000-0000000000a2"
	r, err := repo.Rotate(ctx, s.ID, entity.NewSession{ID: given2, AccountID: acc.ID, RefreshTokenHash: []byte("q"), ExpiresAt: exp}, time.Now())
	require.NoError(t, err)
	assert.Equal(t, given2, r.ID)

	_, err = repo.Create(ctx, entity.NewSession{ID: "bukan-uuid", AccountID: acc.ID, RefreshTokenHash: []byte("z"), ExpiresAt: exp})
	assert.Error(t, err, "ID tidak valid ditolak")
}

// P-10: RevokeAllForAccount returns active ids plus ids revoked inside the window; other accounts untouched.
func TestSessions_RevokeAllForAccountReturnsIDs(t *testing.T) {
	ctx := context.Background()
	pool := setup(t)
	accs := NewAccounts(pool)
	a, err := accs.Create(ctx, entity.NewAccount{EmailHash: []byte("a"), EmailEnc: []byte("e"), EmailMasked: "m"})
	require.NoError(t, err)
	b, err := accs.Create(ctx, entity.NewAccount{EmailHash: []byte("b"), EmailEnc: []byte("e"), EmailMasked: "m"})
	require.NoError(t, err)
	repo := NewSessions(pool)
	exp := time.Now().Add(time.Hour)
	now := time.Now()

	active1, err := repo.Create(ctx, entity.NewSession{AccountID: a.ID, RefreshTokenHash: []byte("1"), ExpiresAt: exp})
	require.NoError(t, err)
	active2, err := repo.Create(ctx, entity.NewSession{AccountID: a.ID, RefreshTokenHash: []byte("2"), ExpiresAt: exp})
	require.NoError(t, err)
	recent, err := repo.Create(ctx, entity.NewSession{AccountID: a.ID, RefreshTokenHash: []byte("3"), ExpiresAt: exp})
	require.NoError(t, err)
	old, err := repo.Create(ctx, entity.NewSession{AccountID: a.ID, RefreshTokenHash: []byte("4"), ExpiresAt: exp})
	require.NoError(t, err)
	other, err := repo.Create(ctx, entity.NewSession{AccountID: b.ID, RefreshTokenHash: []byte("5"), ExpiresAt: exp})
	require.NoError(t, err)
	require.NoError(t, repo.Revoke(ctx, recent.ID, now.Add(-time.Minute)))
	require.NoError(t, repo.Revoke(ctx, old.ID, now.Add(-time.Hour)))

	since := now.Add(-20 * time.Minute)
	ids, err := repo.RevokeAllForAccount(ctx, a.ID, now, since)
	require.NoError(t, err)
	assert.ElementsMatch(t, []string{active1.ID, active2.ID, recent.ID}, ids)
	assert.NotContains(t, ids, other.ID)
	assert.NotContains(t, ids, old.ID)

	again, err := repo.RevokeAllForAccount(ctx, a.ID, now.Add(time.Second), since)
	require.NoError(t, err)
	assert.ElementsMatch(t, ids, again, "pengulangan di dalam jendela mengembalikan id yang sama")

	o, ok, err := repo.FindByTokenHash(ctx, []byte("5"))
	require.NoError(t, err)
	require.True(t, ok)
	assert.False(t, o.Revoked(), "akun lain tidak tersentuh")
}
