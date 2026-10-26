// Package bookingclient implements IBookingClient: Payment's calls to Booking's internal API
// (GET /internal/v1/bookings/{id}/payable and POST .../payment-lock). Each call carries a service token and
// runs through a circuit breaker and a bulkhead (design §6, booking_internal).
package bookingclient

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/tiket-sampai-nonton/rusuh-booking-be/pkg/resilience"
	"github.com/tiket-sampai-nonton/rusuh-booking-be/pkg/telemetry"

	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/payment/internal/payment/port/outbound"
)

const maxResponseBytes = 1 << 20

// shortcut: fixed values from design §6 (timeout 3 s, threshold 5, cooldown 10 s, bulkhead 20); move to config when Booking needs other limits.
const (
	callTimeout      = 3 * time.Second
	failureThreshold = 5
	cooldown         = 10 * time.Second
	maxConcurrent    = 20
)

// Authorizer adds the service bearer token and trace headers to an outgoing request (httpx.ServiceTokenSource).
type Authorizer interface {
	Authorize(req *http.Request) error
}

// errServer marks failures that say Booking is unhealthy (5xx, transport); only these open the breaker.
var errServer = errors.New("booking: server error")

// Client calls Booking.
type Client struct {
	baseURL  string
	auth     Authorizer
	http     *http.Client
	breaker  *resilience.Breaker
	bulkhead *resilience.Bulkhead
}

// New builds the client. httpClient may be nil.
func New(baseURL string, auth Authorizer, httpClient *http.Client, now func() time.Time) (*Client, error) {
	u, err := url.Parse(baseURL)
	if err != nil || u.Host == "" || (u.Scheme != "http" && u.Scheme != "https") {
		return nil, fmt.Errorf("bookingclient: base URL %q tidak valid", baseURL)
	}
	if httpClient == nil {
		httpClient = &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	}
	b, err := resilience.NewBreaker(resilience.BreakerConfig{
		Name: "booking_internal", FailureThreshold: failureThreshold, Cooldown: cooldown, HalfOpenProbes: 1, Timeout: callTimeout,
		IsFailure:     func(err error) bool { return errors.Is(err, errServer) },
		OnStateChange: telemetry.BreakerObserver(),
	}, now)
	if err != nil {
		return nil, err
	}
	bh, err := resilience.NewBulkhead(maxConcurrent, 0)
	if err != nil {
		return nil, err
	}
	return &Client{baseURL: strings.TrimRight(baseURL, "/"), auth: auth, http: httpClient, breaker: b, bulkhead: bh}, nil
}

type payableBody struct {
	BookingID        string    `json:"bookingId"`
	PublicRef        string    `json:"publicRef"`
	Status           string    `json:"status"`
	AmountMinor      int64     `json:"amountMinor"`
	PaymentExpiresAt time.Time `json:"paymentExpiresAt"`
	VisitorID        string    `json:"visitorId"`
	Version          int       `json:"version"`
	CanEdit          bool      `json:"canEdit"`
}

type lockBody struct {
	Outcome          string    `json:"outcome"`
	Version          int       `json:"version"`
	AmountMinor      int64     `json:"amountMinor"`
	PaymentExpiresAt time.Time `json:"paymentExpiresAt"`
}

// GetPayable returns what Payment needs to charge a booking.
func (c *Client) GetPayable(ctx context.Context, bookingID string) (outbound.Payable, error) {
	var out payableBody
	if err := c.do(ctx, http.MethodGet, "/internal/v1/bookings/"+url.PathEscape(bookingID)+"/payable", nil, &out); err != nil {
		return outbound.Payable{}, err
	}
	return outbound.Payable(out), nil
}

// Lock locks the booking before a charge; attemptKey makes a repeated lock for the same attempt idempotent.
func (c *Client) Lock(ctx context.Context, bookingID, attemptKey string) (outbound.PaymentLock, error) {
	var out lockBody
	body := map[string]string{"attemptKey": attemptKey}
	if err := c.do(ctx, http.MethodPost, "/internal/v1/bookings/"+url.PathEscape(bookingID)+"/payment-lock", body, &out); err != nil {
		return outbound.PaymentLock{}, err
	}
	return outbound.PaymentLock(out), nil
}

func (c *Client) do(ctx context.Context, method, path string, in, data any) error {
	err := c.breaker.Do(ctx, func(ctx context.Context) error {
		return c.bulkhead.Do(ctx, func(ctx context.Context) error { return c.call(ctx, method, path, in, data) })
	})
	switch {
	case err == nil, errors.Is(err, outbound.ErrBookingNotFound):
		return err
	case errors.Is(err, context.Canceled):
		return err
	}
	// Breaker open, bulkhead full, timeout, 5xx, or an unexpected 4xx (e.g. a rejected service token).
	return fmt.Errorf("%w: %w", outbound.ErrBookingUnavailable, err)
}

func (c *Client) call(ctx context.Context, method, path string, in, data any) error {
	var body io.Reader
	if in != nil {
		raw, err := json.Marshal(in)
		if err != nil {
			return err
		}
		body = bytes.NewReader(raw)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.baseURL+path, body)
	if err != nil {
		return err
	}
	req.Header.Set("Accept", "application/json")
	if in != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if err := c.auth.Authorize(req); err != nil {
		return fmt.Errorf("service token: %w", err)
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("%w: %w", errServer, err)
	}
	defer func() { _ = resp.Body.Close() }()
	switch {
	case resp.StatusCode == http.StatusNotFound:
		return outbound.ErrBookingNotFound
	case resp.StatusCode >= 500:
		return fmt.Errorf("%w: http %d", errServer, resp.StatusCode)
	case resp.StatusCode != http.StatusOK:
		return fmt.Errorf("booking menolak panggilan: http %d", resp.StatusCode)
	}
	var env struct {
		Data json.RawMessage `json:"data"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, maxResponseBytes)).Decode(&env); err != nil {
		return fmt.Errorf("%w: respons tidak terbaca", errServer)
	}
	if err := json.Unmarshal(env.Data, data); err != nil {
		return fmt.Errorf("%w: data tidak terbaca", errServer)
	}
	return nil
}
