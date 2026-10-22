// Package bookingclient is the HTTP adapter for the Booking Service internal API (design §2.7).
// Every synchronous call is wrapped in a circuit breaker (pkg/resilience, D-40).
package bookingclient

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/tiket-sampai-nonton/rusuh-booking-be/pkg/resilience"

	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/queue/internal/queue/domain/entity"
	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/queue/internal/queue/port/outbound"
)

// Client calls the Booking Service. It implements outbound.IBookingClient.
type Client struct {
	baseURL      string
	http         *http.Client
	breaker      *resilience.Breaker
	serviceToken string // EdDSA-signed JWT from Queue's signer; sent as Bearer on every request
}

// NewClient returns the client. timeout bounds each request; breaker fails fast when Booking is down.
func NewClient(baseURL string, timeout time.Duration, breaker *resilience.Breaker, serviceToken string) *Client {
	return &Client{
		baseURL:      strings.TrimRight(baseURL, "/"),
		http:         &http.Client{Timeout: timeout},
		breaker:      breaker,
		serviceToken: serviceToken,
	}
}

// ReserveBatch reserves allocations for a batch of admitted entries.
func (c *Client) ReserveBatch(ctx context.Context, req outbound.AllocationBatchRequest) (outbound.AllocationBatchResponse, error) {
	var resp outbound.AllocationBatchResponse
	err := c.do(ctx, http.MethodPost, "/internal/v1/allocations:batch", req, &resp)
	if err != nil {
		return outbound.AllocationBatchResponse{}, err
	}
	return resp, nil
}

// Supply returns the current stock snapshot for an event.
func (c *Client) Supply(ctx context.Context, eventID string) (outbound.SupplySnapshot, error) {
	var resp outbound.SupplySnapshot
	err := c.do(ctx, http.MethodGet, "/internal/v1/events/"+eventID+"/supply", nil, &resp)
	if err != nil {
		return outbound.SupplySnapshot{}, err
	}
	return resp, nil
}

// ReleaseAllocation releases an admitted allocation as saga compensation (spec 14.R1, ADR-018).
func (c *Client) ReleaseAllocation(ctx context.Context, allocationID string, reason string) error {
	body := map[string]string{"reason": reason}
	return c.do(ctx, http.MethodPost, "/internal/v1/allocations/"+allocationID+":release", body, nil)
}

func (c *Client) do(ctx context.Context, method, path string, body, out any) error {
	call := func(ctx context.Context) error {
		var rdr io.Reader
		if body != nil {
			raw, err := json.Marshal(body)
			if err != nil {
				return fmt.Errorf("bookingclient: marshal: %w", err)
			}
			rdr = bytes.NewReader(raw)
		}
		httpReq, err := http.NewRequestWithContext(ctx, method, c.baseURL+path, rdr)
		if err != nil {
			return fmt.Errorf("bookingclient: new request: %w", err)
		}
		if body != nil {
			httpReq.Header.Set("Content-Type", "application/json")
		}
		httpReq.Header.Set("Accept", "application/json")
		if c.serviceToken != "" {
			httpReq.Header.Set("Authorization", "Bearer "+c.serviceToken)
		}
		res, err := c.http.Do(httpReq)
		if err != nil {
			return fmt.Errorf("bookingclient: %s %s: %w", method, path, err)
		}
		defer func() { _ = res.Body.Close() }()
		if res.StatusCode >= http.StatusInternalServerError {
			return fmt.Errorf("bookingclient: %s %s: status %d", method, path, res.StatusCode)
		}
		if res.StatusCode >= http.StatusBadRequest {
			return fmt.Errorf("bookingclient: %s %s: status %d", method, path, res.StatusCode)
		}
		if out != nil {
			// Booking uses the httpx envelope: {"meta":{...},"data":{...}}
			// We need to unwrap the "data" field before decoding into the target.
			var envelope struct {
				Data json.RawMessage `json:"data"`
			}
			if err := json.NewDecoder(res.Body).Decode(&envelope); err != nil {
				return fmt.Errorf("bookingclient: decode envelope: %w", err)
			}
			if len(envelope.Data) > 0 {
				if err := json.Unmarshal(envelope.Data, out); err != nil {
					return fmt.Errorf("bookingclient: decode data: %w", err)
				}
			}
		}
		return nil
	}
	if c.breaker == nil {
		if err := call(ctx); err != nil {
			return wrapDependency(err)
		}
		return nil
	}
	if err := c.breaker.Do(ctx, call); err != nil {
		return wrapDependency(err)
	}
	return nil
}

// wrapDependency marks transport/breaker failures so the usecase can map them to 503.
func wrapDependency(err error) error {
	return fmt.Errorf("%w: %s", entity.ErrDependencyUnavailable, err.Error())
}
