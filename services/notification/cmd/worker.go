package main

import (
	"github.com/spf13/cobra"

	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/notification/internal/delivery"
)

var workerCmd = &cobra.Command{
	Use:   "worker",
	Short: "Jalankan proses latar: consumer booking.*, queue.turn.* → email/push",
	RunE: func(cmd *cobra.Command, _ []string) error {
		return delivery.RunWorker(cmd.Context(), state.cfg, state.logger, delivery.Need{Postgres: true, RabbitMQ: true})
	},
}
