package main

import (
	"github.com/spf13/cobra"

	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/payment/internal/delivery"
)

var workerCmd = &cobra.Command{
	Use:   "worker",
	Short: "Jalankan proses latar: rekonsiliasi attempt UNKNOWN, outbox relay, consumer booking.review",
	RunE: func(cmd *cobra.Command, _ []string) error {
		return delivery.RunWorker(cmd.Context(), state.cfg, state.logger, delivery.Need{Postgres: true, RabbitMQ: true})
	},
}
