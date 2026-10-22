// Package paymentclient is the outbound HTTP adapter to the Payment Service (resolve during expiry).
package paymentclient

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/tiket-sampai-nonton/rusuh-booking-be/pkg/httpx"
	inbound "github.com/tiket-sampai-nonton/rusuh-booking-be/services/booking/internal/booking/port/inbound"
	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/booking/internal/booking/port/outbound"
)

// Client calls POST /internal/v1/payments/{bookingId}/resolve on the Payment Service.
type Client struct {
	baseURL      string
	serviceToken string
	http         *http.Client
}

// New builds the client. serviceToken is the EdDSA service bearer (ADR-011).
func New(baseURL, serviceToken string) *Client {
	return &Client{
		baseURL:      strings.TrimRight(baseURL, "/"),
		serviceToken: serviceToken,
		http:         &http.Client{Timeout: 5 * time.Second},
	}
}

type resolveResponse struct {
	Paid bool `json:"paid"`
}

// Resolve asks the Payment Service whether the booking was in fact paid at the gateway.
func (c *Client) Resolve(ctx context.Context, bookingID string) (outbound.ResolveResult, error) {
	url := fmt.Sprintf("%s/internal/v1/payments/%s/resolve", c.baseURL, bookingID)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, nil)
	if err != nil {
		return outbound.ResolveResult{}, err
	}
	if c.serviceToken != "" {
		req.Header.Set("Authorization", "Bearer "+c.serviceToken)
	}
	req.Header.Set("Content-Type", "application/json")
	httpx.Propagate(ctx, req)

	resp, err := c.http.Do(req)
	if err != nil {
		return outbound.ResolveResult{}, inbound.ErrDependency
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode >= http.StatusInternalServerError {
		return outbound.ResolveResult{}, inbound.ErrDependency
	}
	if resp.StatusCode == http.StatusNotFound {
		return outbound.ResolveResult{Paid: false}, nil
	}
	var body struct {
		Data resolveResponse `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		return outbound.ResolveResult{}, inbound.ErrDependency
	}
	return outbound.ResolveResult{Paid: body.Data.Paid}, nil
}
