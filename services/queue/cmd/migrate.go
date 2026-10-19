package main

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/tiket-sampai-nonton/rusuh-booking-be/pkg/migrate"
	"github.com/tiket-sampai-nonton/rusuh-booking-be/pkg/postgres"

	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/queue/deployments/migrations"
)

var (
	upSteps   int
	downSteps int
)

var migrateCmd = &cobra.Command{
	Use:   "migrate",
	Short: "Migrasi schema queue (tertanam di binary, dikunci advisory lock)",
}

var migrateUpCmd = &cobra.Command{
	Use:   "up",
	Short: "Terapkan migrasi yang belum (semua, atau --steps N)",
	RunE: func(cmd *cobra.Command, _ []string) error {
		return withRunner(cmd, func(r *migrate.Runner) error {
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
				fmt.Fprintf(cmd.OutOrStdout(), "%04d_%s\t%s\n", s.Version, s.Name, mark)
			}
			return err
		})
	},
}

func init() {
	migrateUpCmd.Flags().IntVar(&upSteps, "steps", 0, "jumlah migrasi (0 = semua yang belum)")
	migrateDownCmd.Flags().IntVar(&downSteps, "steps", 1, "jumlah migrasi yang di-rollback")
	migrateCmd.AddCommand(migrateUpCmd, migrateDownCmd, migrateStatusCmd)
}

func withRunner(cmd *cobra.Command, fn func(*migrate.Runner) error) error {
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
	return fn(r)
}
