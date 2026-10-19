package main

import (
	"fmt"
	"net/http"
	"time"

	"github.com/spf13/cobra"
)

// healthcheckCmd dipakai HEALTHCHECK container (image distroless tanpa curl).
var healthcheckCmd = &cobra.Command{
	Use:   "healthcheck",
	Short: "Cek GET /health lokal; exit 0 bila sehat",
	RunE: func(cmd *cobra.Command, _ []string) error {
		client := http.Client{Timeout: 3 * time.Second}
		resp, err := client.Get(fmt.Sprintf("http://127.0.0.1:%d/health", state.cfg.App.Port))
		if err != nil {
			return err
		}
		defer func() { _ = resp.Body.Close() }()
		if resp.StatusCode != http.StatusOK {
			return fmt.Errorf("health status %d", resp.StatusCode)
		}
		return nil
	},
}
