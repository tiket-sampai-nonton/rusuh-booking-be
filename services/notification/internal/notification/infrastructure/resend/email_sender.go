// Package resend implements IEmailSender over the Resend HTTP API (demo email). Local and CI use the
// SMTP sender (Mailpit) instead; the provider is chosen in the composition root.
package resend

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"time"

	"github.com/tiket-sampai-nonton/rusuh-booking-be/pkg/rabbitmq"

	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/notification/internal/notification/port/outbound"
)

const (
	defaultBaseURL = "https://api.resend.com"
	defaultTimeout = 10 * time.Second
	maxBody        = 64 << 10
)

// Config holds the Resend settings. APIKey is a secret and is only ever sent in the Authorization
// header; it must never appear in an error or log. BaseURL exists so tests can point at httptest.
type Config struct {
	APIKey  string
	From    string
	BaseURL string
	Timeout time.Duration
}

// Sender implements outbound.IEmailSender.
type Sender struct {
	cfg    Config
	client *http.Client
}

// New returns the Resend sender.
func New(cfg Config) *Sender {
	if cfg.BaseURL == "" {
		cfg.BaseURL = defaultBaseURL
	}
	if cfg.Timeout <= 0 {
		cfg.Timeout = defaultTimeout
	}
	return &Sender{cfg: cfg, client: &http.Client{Timeout: cfg.Timeout}}
}

type request struct {
	From    string   `json:"from"`
	To      []string `json:"to"`
	Subject string   `json:"subject"`
	Text    string   `json:"text"`
	HTML    string   `json:"html,omitempty"`
	// Attachments carry inline images: content_id is the CID the HTML refers to.
	Attachments []attachment `json:"attachments,omitempty"`
}

type attachment struct {
	Filename    string `json:"filename"`
	Content     string `json:"content"` // base64
	ContentType string `json:"content_type"`
	ContentID   string `json:"content_id"`
}

type response struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

// errName only lets short slugs such as "validation_error" through. Resend's "message" field is never
// used: for a 403 it can contain the account owner's email address.
var errName = regexp.MustCompile(`^[a-z_]{1,40}$`)

// SendEmail posts one plain-text email and returns the Resend email id. idempotencyKey makes a retry
// within 24 hours a no-op on the provider side. 400/401/403/422 are permanent (rabbitmq.Permanent);
// 429, 5xx, timeouts and network errors are transient.
func (s *Sender) SendEmail(ctx context.Context, to, subject, body, html, idempotencyKey string, images ...outbound.InlineImage) (string, error) {
	r := request{From: s.cfg.From, To: []string{to}, Subject: subject, Text: body, HTML: html}
	for _, im := range images {
		r.Attachments = append(r.Attachments, attachment{Filename: im.CID + ".png", Content: base64.StdEncoding.EncodeToString(im.Data), ContentType: im.ContentType, ContentID: im.CID})
	}
	payload, err := json.Marshal(r)
	if err != nil {
		return "", fmt.Errorf("resend: encode: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, s.cfg.BaseURL+"/emails", bytes.NewReader(payload))
	if err != nil {
		return "", fmt.Errorf("resend: request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+s.cfg.APIKey)
	req.Header.Set("Content-Type", "application/json")
	if idempotencyKey != "" {
		req.Header.Set("Idempotency-Key", idempotencyKey)
	}

	resp, err := s.client.Do(req)
	if err != nil {
		return "", fmt.Errorf("resend: kirim: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	var out response
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, maxBody))
	_ = json.Unmarshal(raw, &out)

	if resp.StatusCode/100 == 2 {
		if out.ID == "" {
			return "", fmt.Errorf("resend: status %d tanpa id", resp.StatusCode)
		}
		return out.ID, nil
	}

	statusErr := fmt.Errorf("resend: status %d", resp.StatusCode)
	if errName.MatchString(out.Name) {
		statusErr = fmt.Errorf("resend: status %d %s", resp.StatusCode, out.Name)
	}
	switch resp.StatusCode {
	case http.StatusBadRequest, http.StatusUnauthorized, http.StatusForbidden, http.StatusUnprocessableEntity:
		return "", rabbitmq.Permanent(statusErr)
	}
	return "", statusErr
}
