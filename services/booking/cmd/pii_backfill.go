package main

import (
	"github.com/spf13/cobra"

	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/booking/internal/delivery"
)

var piiBackfillCmd = &cobra.Command{
	Use:   "pii-backfill",
	Short: "Enkripsi email pembeli lama (plaintext) dan kosongkan kolom plaintext; idempoten (PDP-1)",
	RunE: func(cmd *cobra.Command, _ []string) error {
		return delivery.PIIBackfill(cmd.Context(), state.cfg, state.logger)
	},
}
