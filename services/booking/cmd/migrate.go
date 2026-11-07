package main

import (
	"context"
	"errors"
	"fmt"
	"regexp"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/spf13/cobra"

	"github.com/tiket-sampai-nonton/rusuh-booking-be/pkg/migrate"
	"github.com/tiket-sampai-nonton/rusuh-booking-be/pkg/postgres"

	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/booking/deployments/migrations"
)

var (
	upSteps         int
	downSteps       int
	allowLiveAdmits bool
)

// anonymousAdmissionVersion is the migration whose cutover must happen between sales (ADR-030).
const anonymousAdmissionVersion = 9

// rowCounter is the minimal read-only query surface the live-admission guard needs.
type rowCounter interface {
	CountLiveAdmissions(ctx context.Context, schema string) (int, error)
}

var schemaName = regexp.MustCompile(`^[a-z_][a-z0-9_]*$`)

// guardLiveAdmissions refuses migration 0009 while live admissions exist, unless explicitly allowed.
// It runs only when 0009 is pending, so re-running `up` after it was applied never trips it.
func guardLiveAdmissions(ctx context.Context, q rowCounter, schema string, pending, allow bool) error {
	if !pending || allow {
		return nil
	}
	n, err := q.CountLiveAdmissions(ctx, schema)
	if err != nil {
		return err
	}
	if n > 0 {
		return fmt.Errorf("migration 0009 changes allocated_units release semantics; %d live admissions exist; run between sales or pass --allow-live-admissions", n)
	}
	return nil
}

// poolCounter counts live admissions with a read-only query on the configured schema.
type poolCounter struct{ pool *pgxpool.Pool }

func (c poolCounter) CountLiveAdmissions(ctx context.Context, schema string) (int, error) {
	if !schemaName.MatchString(schema) {
		return 0, errors.New("schema name tidak valid")
	}
	var n int
	// The admissions table may not exist yet on a brand-new database: that means zero live rows.
	var exists bool
	if err := c.pool.QueryRow(ctx, `SELECT to_regclass($1) IS NOT NULL`, schema+".admissions").Scan(&exists); err != nil {
		return 0, err
	}
	if !exists {
		return 0, nil
	}
	err := c.pool.QueryRow(ctx, `SELECT count(*) FROM `+schema+`.admissions WHERE status IN ('RESERVED','ACTIVE','EDITABLE')`).Scan(&n)
	return n, err
}

var migrateCmd = &cobra.Command{
	Use:   "migrate",
	Short: "Migrasi schema booking (tertanam di binary, dikunci advisory lock)",
}

var migrateUpCmd = &cobra.Command{
	Use:   "up",
	Short: "Terapkan migrasi yang belum (semua, atau --steps N)",
	RunE: func(cmd *cobra.Command, _ []string) error {
		return withRunnerPool(cmd, func(r *migrate.Runner, pool *pgxpool.Pool) error {
			st, err := r.Status(cmd.Context())
			if err != nil {
				return err
			}
			pending := false
			for _, m := range st {
				if m.Version == anonymousAdmissionVersion && !m.Applied {
					pending = true
				}
			}
			if err := guardLiveAdmissions(cmd.Context(), poolCounter{pool}, state.cfg.Postgres.Schema, pending, allowLiveAdmits); err != nil {
				return err
			}
			n, err := r.Up(cmd.Context(), upSteps)
			state.logger.Info("migrate up selesai", "applied", n)
			return err
		})
	},
}

var migrateDownCmd = &cobra.Command{
	Use:   "down",
	Short: "Rollback migrasi terbaru (default 1, atau --steps N)",
	RunE: func(cmd *cobra.Command, _ []string) error {
		return withRunner(cmd, func(r *migrate.Runner) error {
			n, err := r.Down(cmd.Context(), downSteps)
			state.logger.Info("migrate down selesai", "rolledBack", n)
			return err
		})
	},
}

var migrateStatusCmd = &cobra.Command{
	Use:   "status",
	Short: "Tampilkan status migrasi",
	RunE: func(cmd *cobra.Command, _ []string) error {
		return withRunner(cmd, func(r *migrate.Runner) error {
			st, err := r.Status(cmd.Context())
			for _, s := range st {
				mark := "pending"
				if s.Applied {
					mark = "applied " + s.AppliedAt.UTC().Format("2006-01-02T15:04:05Z")
				}
				_, _ = fmt.Fprintf(cmd.OutOrStdout(), "%04d_%s\t%s\n", s.Version, s.Name, mark)
			}
			return err
		})
	},
}

func init() {
	migrateUpCmd.Flags().IntVar(&upSteps, "steps", 0, "jumlah migrasi (0 = semua yang belum)")
	migrateUpCmd.Flags().BoolVar(&allowLiveAdmits, "allow-live-admissions", false, "izinkan migrasi 0009 walau ada admission aktif (cutover di luar jendela penjualan)")
	migrateDownCmd.Flags().IntVar(&downSteps, "steps", 1, "jumlah migrasi yang di-rollback")
	migrateCmd.AddCommand(migrateUpCmd, migrateDownCmd, migrateStatusCmd)
}

func withRunner(cmd *cobra.Command, fn func(*migrate.Runner) error) error {
	return withRunnerPool(cmd, func(r *migrate.Runner, _ *pgxpool.Pool) error { return fn(r) })
}

func withRunnerPool(cmd *cobra.Command, fn func(*migrate.Runner, *pgxpool.Pool) error) error {
	pg, err := postgres.New(cmd.Context(), state.cfg.Postgres)
	if err != nil {
		return err
	}
	defer pg.Close()
	migs, err := migrate.Load(migrations.FS, ".")
	if err != nil {
		return err
	}
	r, err := migrate.NewRunner(pg.Pool(), state.cfg.Postgres.Schema, migs, state.logger)
	if err != nil {
		return err
	}
	return fn(r, pg.Pool())
}
