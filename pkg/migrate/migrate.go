// Package migrate runs versioned SQL migrations embedded in the service binary.
//
// Setiap migrasi berupa pasangan NNNN_nama.up.sql dan NNNN_nama.down.sql. Satu migrasi dijalankan
// dalam satu transaksi bersama baris pelacaknya di <schema>.schema_migrations, sehingga kegagalan
// tidak meninggalkan schema setengah jadi. Seluruh proses dikunci pg_advisory_lock per schema agar
// dua instance yang start bersamaan tidak menjalankan migrasi ganda (D-16). Checksum file yang sudah
// diterapkan diperiksa: mengubah migrasi lama ditolak, buat migrasi baru.
package migrate

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"path"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

var (
	filePattern   = regexp.MustCompile(`^(\d{4})_([a-z0-9_]+)\.(up|down)\.sql$`)
	schemaPattern = regexp.MustCompile(`^[a-z_][a-z0-9_]{0,62}$`)
)

// Migration is one versioned pair.
type Migration struct {
	Version  int
	Name     string
	Up       string
	Down     string
	Checksum string // sha256 dari Up
}

// Status is the state of one migration.
type Status struct {
	Version   int
	Name      string
	Applied   bool
	AppliedAt time.Time
}

// Load reads every migration pair from dir in fsys, sorted by version.
func Load(fsys fs.FS, dir string) ([]Migration, error) {
	entries, err := fs.ReadDir(fsys, dir)
	if err != nil {
		return nil, fmt.Errorf("migrate: read %s: %w", dir, err)
	}
	byVersion := map[int]*Migration{}
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".sql") {
			continue
		}
		m := filePattern.FindStringSubmatch(e.Name())
		if m == nil {
			return nil, fmt.Errorf("migrate: nama file %q harus NNNN_nama.up.sql atau NNNN_nama.down.sql", e.Name())
		}
		version, _ := strconv.Atoi(m[1])
		body, err := fs.ReadFile(fsys, path.Join(dir, e.Name()))
		if err != nil {
			return nil, fmt.Errorf("migrate: read %s: %w", e.Name(), err)
		}
		mig := byVersion[version]
		if mig == nil {
			mig = &Migration{Version: version, Name: m[2]}
			byVersion[version] = mig
		} else if mig.Name != m[2] {
			return nil, fmt.Errorf("migrate: versi %04d punya dua nama (%s, %s)", version, mig.Name, m[2])
		}
		if m[3] == "up" {
			mig.Up = string(body)
			sum := sha256.Sum256(body)
			mig.Checksum = hex.EncodeToString(sum[:])
		} else {
			mig.Down = string(body)
		}
	}
	out := make([]Migration, 0, len(byVersion))
	for _, m := range byVersion {
		if strings.TrimSpace(m.Up) == "" || strings.TrimSpace(m.Down) == "" {
			return nil, fmt.Errorf("migrate: %04d_%s wajib punya .up.sql dan .down.sql yang tidak kosong", m.Version, m.Name)
		}
		out = append(out, *m)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Version < out[j].Version })
	for i, m := range out {
		if m.Version != i+1 {
			return nil, fmt.Errorf("migrate: versi harus berurutan mulai 0001; ditemukan %04d di posisi %d", m.Version, i+1)
		}
	}
	return out, nil
}

// Runner applies migrations to one schema.
type Runner struct {
	pool       *pgxpool.Pool
	schema     string
	migrations []Migration
	logger     *slog.Logger
}

// NewRunner validates the schema name and returns a runner.
func NewRunner(pool *pgxpool.Pool, schema string, migrations []Migration, logger *slog.Logger) (*Runner, error) {
	if !schemaPattern.MatchString(schema) {
		return nil, fmt.Errorf("migrate: schema %q tidak valid", schema)
	}
	return &Runner{pool: pool, schema: schema, migrations: migrations, logger: logger}, nil
}

// LockKey is the advisory lock name for a schema.
func LockKey(schema string) string { return "rbt.migrate." + schema }

func (r *Runner) withLock(ctx context.Context, fn func(*pgxpool.Conn) error) error {
	conn, err := r.pool.Acquire(ctx)
	if err != nil {
		return fmt.Errorf("migrate: acquire: %w", err)
	}
	defer conn.Release()
	if _, err := conn.Exec(ctx, `SELECT pg_advisory_lock(hashtextextended($1, 0))`, LockKey(r.schema)); err != nil {
		return fmt.Errorf("migrate: advisory lock: %w", err)
	}
	defer func() {
		_, _ = conn.Exec(context.Background(), `SELECT pg_advisory_unlock(hashtextextended($1, 0))`, LockKey(r.schema))
	}()
	if _, err := conn.Exec(ctx, fmt.Sprintf(`CREATE TABLE IF NOT EXISTS %s.schema_migrations (
		version integer PRIMARY KEY,
		name text NOT NULL,
		checksum text NOT NULL,
		applied_at timestamptz NOT NULL DEFAULT now())`, r.schema)); err != nil {
		return fmt.Errorf("migrate: ensure %s.schema_migrations: %w", r.schema, err)
	}
	return fn(conn)
}

type applied struct {
	checksum string
	at       time.Time
}

func (r *Runner) applied(ctx context.Context, conn *pgxpool.Conn) (map[int]applied, error) {
	rows, err := conn.Query(ctx, fmt.Sprintf(`SELECT version, checksum, applied_at FROM %s.schema_migrations`, r.schema))
	if err != nil {
		return nil, fmt.Errorf("migrate: read tracking: %w", err)
	}
	defer rows.Close()
	out := map[int]applied{}
	for rows.Next() {
		var v int
		var a applied
		if err := rows.Scan(&v, &a.checksum, &a.at); err != nil {
			return nil, err
		}
		out[v] = a
	}
	return out, rows.Err()
}

func (r *Runner) checkDrift(done map[int]applied) error {
	known := map[int]Migration{}
	for _, m := range r.migrations {
		known[m.Version] = m
	}
	for v, a := range done {
		m, ok := known[v]
		if !ok {
			return fmt.Errorf("migrate: versi %04d sudah diterapkan di database tetapi tidak ada di binary", v)
		}
		if m.Checksum != a.checksum {
			return fmt.Errorf("migrate: %04d_%s berubah setelah diterapkan; buat migrasi baru, jangan ubah yang lama", v, m.Name)
		}
	}
	return nil
}

// Up applies pending migrations in order. steps <= 0 applies all.
func (r *Runner) Up(ctx context.Context, steps int) (int, error) {
	n := 0
	err := r.withLock(ctx, func(conn *pgxpool.Conn) error {
		done, err := r.applied(ctx, conn)
		if err != nil {
			return err
		}
		if err := r.checkDrift(done); err != nil {
			return err
		}
		for _, m := range r.migrations {
			if _, ok := done[m.Version]; ok {
				continue
			}
			if steps > 0 && n >= steps {
				break
			}
			if err := r.exec(ctx, conn, m.Up, fmt.Sprintf(`INSERT INTO %s.schema_migrations (version, name, checksum) VALUES ($1, $2, $3)`, r.schema),
				m.Version, m.Name, m.Checksum); err != nil {
				return fmt.Errorf("migrate: up %04d_%s: %w", m.Version, m.Name, err)
			}
			r.logger.Info("migration applied", "schema", r.schema, "version", m.Version, "name", m.Name)
			n++
		}
		return nil
	})
	return n, err
}

// Down rolls back the newest applied migrations. steps <= 0 means 1 (tidak pernah semua tanpa sengaja).
func (r *Runner) Down(ctx context.Context, steps int) (int, error) {
	if steps <= 0 {
		steps = 1
	}
	n := 0
	err := r.withLock(ctx, func(conn *pgxpool.Conn) error {
		done, err := r.applied(ctx, conn)
		if err != nil {
			return err
		}
		if err := r.checkDrift(done); err != nil {
			return err
		}
		for i := len(r.migrations) - 1; i >= 0 && n < steps; i-- {
			m := r.migrations[i]
			if _, ok := done[m.Version]; !ok {
				continue
			}
			if err := r.exec(ctx, conn, m.Down, fmt.Sprintf(`DELETE FROM %s.schema_migrations WHERE version = $1`, r.schema), m.Version); err != nil {
				return fmt.Errorf("migrate: down %04d_%s: %w", m.Version, m.Name, err)
			}
			r.logger.Info("migration rolled back", "schema", r.schema, "version", m.Version, "name", m.Name)
			n++
		}
		return nil
	})
	return n, err
}

// Status lists every known migration and whether it is applied.
func (r *Runner) Status(ctx context.Context) ([]Status, error) {
	var out []Status
	err := r.withLock(ctx, func(conn *pgxpool.Conn) error {
		done, err := r.applied(ctx, conn)
		if err != nil {
			return err
		}
		for _, m := range r.migrations {
			a, ok := done[m.Version]
			out = append(out, Status{Version: m.Version, Name: m.Name, Applied: ok, AppliedAt: a.at})
		}
		return r.checkDrift(done)
	})
	return out, err
}

func (r *Runner) exec(ctx context.Context, conn *pgxpool.Conn, ddl, track string, args ...any) error {
	tx, err := conn.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err := tx.Exec(ctx, fmt.Sprintf(`SET LOCAL search_path = %s`, r.schema)); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, ddl); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, track, args...); err != nil {
		return err
	}
	if err := tx.Commit(ctx); err != nil && !errors.Is(err, pgx.ErrTxClosed) {
		return err
	}
	return nil
}
