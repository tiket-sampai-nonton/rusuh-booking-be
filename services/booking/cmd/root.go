package main

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"github.com/spf13/cobra"

	"github.com/tiket-sampai-nonton/rusuh-booking-be/pkg/dotenv"
	"github.com/tiket-sampai-nonton/rusuh-booking-be/pkg/logger"

	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/booking/internal/config"
)

// app holds what PersistentPreRunE prepares for every verb.
type app struct {
	cfg    config.Config
	logger *slog.Logger
}

var state app

var rootCmd = &cobra.Command{
	Use:           "booking",
	Short:         "Booking Service — Rusuh Booking Tiket",
	SilenceUsage:  true,
	SilenceErrors: true,
	PersistentPreRunE: func(cmd *cobra.Command, _ []string) error {
		if err := dotenv.Load(".env"); err != nil {
			return err
		}
		cfg, err := config.Load()
		if err != nil {
			return err
		}
		l, err := logger.New(os.Stdout, cfg.Log, config.ServiceName, cmd.Name())
		if err != nil {
			return err
		}
		slog.SetDefault(l)
		state = app{cfg: cfg, logger: l}
		return nil
	},
}

func init() {
	rootCmd.AddCommand(apiCmd, migrateCmd, healthcheckCmd, workerCmd)
}

// Execute runs the command tree with a context cancelled on SIGINT/SIGTERM.
func Execute() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := rootCmd.ExecuteContext(ctx); err != nil {
		fmt.Fprintln(os.Stderr, "booking:", err)
		stop()
		os.Exit(1)
	}
}
