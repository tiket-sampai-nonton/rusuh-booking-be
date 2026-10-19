package main

import (
	"github.com/spf13/cobra"

	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/payment/internal/delivery"
)

var apiCmd = &cobra.Command{
	Use:   "api",
	Short: "Jalankan HTTP API",
	RunE: func(cmd *cobra.Command, _ []string) error {
		return delivery.RunServer(cmd.Context(), state.cfg, state.logger, delivery.Need{Postgres: true, RabbitMQ: true})
	},
}
