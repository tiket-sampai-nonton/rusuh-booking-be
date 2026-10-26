// Package paymentclient is the outbound HTTP adapter to the Payment Service: POST /internal/v1/payments/{id}/resolve
// (internal-payment.yaml). It is used when a booking expires or the buyer cancels.
package paymentclient

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/booking/internal/booking/port/inbound"
	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/booking/internal/booking/port/outbound"
)

// Authorizer adds the service bearer token (short-lived, ADR-011) and the request/trace headers to an
// outgoing request. In production it is httpx.ServiceTokenSource.
type Authorizer interface {
	Authorize(req *http.Request) error
}

// Client calls the Payment Service.
type Client struct {
	baseURL string
	auth    Authorizer
	http    *http.Client
}

// New builds the client.
func New(baseURL string, auth Authorizer) *Client {
	return &Client{
		baseURL: strings.TrimRight(baseURL, "/"),
		auth:    auth,
		http:    &http.Client{Timeout: 5 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }},
	}
}

type resolveBody struct {
	Outcome        string     `json:"outcome"`
	SettlementTime *time.Time `json:"settlementTime"`
}

// Resolve asks Payment about the booking's payment. Any failure to get a clear answer (transport
// error, non-200, unreadable or unknown outcome) is inbound.ErrDependency: the caller keeps the
// stock held and tries again.
func (c *Client) Resolve(ctx context.Context, bookingID string, mode outbound.ResolveMode) (outbound.ResolveResult, error) {
	raw, _ := json.Marshal(map[string]string{"mode": string(mode)})
	endpoint := fmt.Sprintf("%s/internal/v1/payments/%s/resolve", c.baseURL, url.PathEscape(bookingID))
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(raw))
	if err != nil {
		return outbound.ResolveResult{}, err
	}
	req.Header.Set("Content-Type", "application/json")
	if c.auth != nil {
		if err := c.auth.Authorize(req); err != nil {
			slog.ErrorContext(ctx, "service token Payment gagal dibuat", "error", err)
			return outbound.ResolveResult{}, inbound.ErrDependency
		}
	}

	resp, err := c.http.Do(req)
	if err != nil {
		return outbound.ResolveResult{}, inbound.ErrDependency
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		slog.WarnContext(ctx, "resolve ditolak Payment", "status", resp.StatusCode)
		return outbound.ResolveResult{}, inbound.ErrDependency
	}
	var body struct {
		Data resolveBody `json:"data"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&body); err != nil {
		return outbound.ResolveResult{}, inbound.ErrDependency
	}
	switch o := outbound.ResolveOutcome(body.Data.Outcome); o {
	case outbound.OutcomeSucceeded, outbound.OutcomeNotPaid, outbound.OutcomeNoAttempt, outbound.OutcomeUnknown:
		return outbound.ResolveResult{Outcome: o, SettlementTime: body.Data.SettlementTime}, nil
	}
	return outbound.ResolveResult{}, inbound.ErrDependency
}
