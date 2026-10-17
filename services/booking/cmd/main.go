// Command booking runs the Booking Service.
//
// Usage:
//
//	booking --mode=api|worker
//	booking --healthcheck   # dipakai HEALTHCHECK container (image distroless tanpa curl)
package main

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/booking/internal/config"
	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/booking/internal/delivery"
)

func main() {
	os.Exit(run())
}

func run() int {
	mode := flag.String("mode", "api", "mode proses: api | worker")
	healthcheck := flag.Bool("healthcheck", false, "cek /healthz lokal lalu keluar (exit 0 bila sehat)")
	flag.Parse()

	cfg, err := config.Load()
	if err != nil {
		fmt.Fprintln(os.Stderr, "config:", err)
		return 1
	}
	if *healthcheck {
		return probe(cfg.Port)
	}

	logger := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: cfg.LogLevel})).
		With("service", cfg.ServiceName, "mode", *mode)
	slog.SetDefault(logger)

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	switch *mode {
	case "api":
		err = delivery.RunServer(ctx, cfg, logger)
	case "worker":
		err = delivery.RunWorker(ctx, cfg, logger)
	default:
		err = fmt.Errorf("mode %q tidak dikenal (pilihan: api | worker)", *mode)
	}
	if err != nil {
		logger.Error("exit with error", "error", err)
		return 1
	}
	return 0
}

func probe(port int) int {
	client := http.Client{Timeout: 2 * time.Second}
	resp, err := client.Get(fmt.Sprintf("http://127.0.0.1:%d/healthz", port))
	if err != nil {
		return 1
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return 1
	}
	return 0
}
