package main

import (
	"github.com/spf13/cobra"

	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/queue/internal/delivery"
)

var workerCmd = &cobra.Command{
	Use:   "worker",
	Short: "Jalankan proses latar: promotor admission, publisher Status Document, consumer booking.*",
	RunE: func(cmd *cobra.Command, _ []string) error {
		return delivery.RunWorker(cmd.Context(), state.cfg, state.logger, delivery.Need{Postgres: true, Valkey: true, RabbitMQ: true})
	},
}
