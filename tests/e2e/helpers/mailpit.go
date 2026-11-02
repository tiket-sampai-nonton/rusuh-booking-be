//go:build e2e

package helpers

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"regexp"
	"time"
)

// ReadOTP fetches the most recent email for toEmail from Mailpit and extracts a 6-digit OTP code.
// Polls up to 10 seconds.
func ReadOTP(ctx context.Context, mailpitURL, toEmail string) (string, error) {
	deadline := time.Now().Add(10 * time.Second)
	otpRe := regexp.MustCompile(`\b(\d{6})\b`)
	for time.Now().Before(deadline) {
		resp, err := http.Get(mailpitURL + "/api/v1/messages")
		if err != nil {
			time.Sleep(500 * time.Millisecond)
			continue
		}
		var body struct {
			Messages []struct {
				To      []struct{ Address string } `json:"To"`
				Snippet string                     `json:"Snippet"`
			} `json:"messages"`
		}
		_ = json.NewDecoder(resp.Body).Decode(&body)
		resp.Body.Close()
		for _, m := range body.Messages {
			for _, t := range m.To {
				if t.Address == toEmail {
					if matches := otpRe.FindStringSubmatch(m.Snippet); len(matches) > 1 {
						return matches[1], nil
					}
				}
			}
		}
		time.Sleep(500 * time.Millisecond)
	}
	return "", fmt.Errorf("OTP not found in Mailpit for %s within 10s", toEmail)
}
