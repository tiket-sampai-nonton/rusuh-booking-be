// Package midtrans implements IPaymentGateway on Midtrans Snap (POST /snap/v1/transactions) plus the Core API order
// endpoints (GET /v2/{id}/status, POST /v2/{id}/cancel). Request and response shapes were checked against docs.midtrans.com on 2026-10-26;
// see docs/evidence/2026-10-26-midtrans-verifikasi.md. Every call runs through a circuit breaker and a bulkhead.
package midtrans

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/tiket-sampai-nonton/rusuh-booking-be/pkg/resilience"
	"github.com/tiket-sampai-nonton/rusuh-booking-be/pkg/telemetry"

	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/payment/internal/payment/domain/entity"
	vo "github.com/tiket-sampai-nonton/rusuh-booking-be/services/payment/internal/payment/domain/valueobject"
	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/payment/internal/payment/port/outbound"
)

const maxResponseBytes = 1 << 20

// Limits sets one breaker and bulkhead (design §6).
type Limits struct {
	Timeout          time.Duration
	FailureThreshold int
	Cooldown         time.Duration
	Bulkhead         int
}

// Config configures the gateway. ServerKey is a secret: it is only used for the Authorization header.
type Config struct {
	BaseURL string
	// SnapBaseURL hosts POST /snap/v1/transactions; empty derives it from BaseURL (api. -> app.).
	SnapBaseURL string
	ServerKey   string
	// NotificationURL, when set, is sent as X-Override-Notification on every charge so Midtrans posts that
	// transaction's notifications there instead of the dashboard URL. Meant for local tests through a tunnel;
	// leave it empty in staging and production.
	NotificationURL string
	Charge          Limits // POST /v2/charge
	Status          Limits // status check and cancel
	Client          *http.Client
}

type guard struct {
	breaker  *resilience.Breaker
	bulkhead *resilience.Bulkhead
}

// Gateway talks to Midtrans.
type Gateway struct {
	baseURL   string
	snapURL   string
	auth      string
	notifyURL string
	client    *http.Client
	charge    guard
	status    guard
}

// New builds the gateway. It never logs or returns the server key.
func New(cfg Config, now func() time.Time) (*Gateway, error) {
	u, err := url.Parse(cfg.BaseURL)
	if err != nil || u.Host == "" || (u.Scheme != "https" && u.Scheme != "http") {
		return nil, fmt.Errorf("midtrans: base URL %q tidak valid", cfg.BaseURL)
	}
	if cfg.ServerKey == "" {
		return nil, errors.New("midtrans: server key kosong")
	}
	client := cfg.Client
	if client == nil {
		// Redirects are not followed: Midtrans never redirects the API, and the Authorization header must not leave the host.
		client = &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	}
	cg, err := newGuard("midtrans_charge", cfg.Charge, now)
	if err != nil {
		return nil, err
	}
	sg, err := newGuard("midtrans_status", cfg.Status, now)
	if err != nil {
		return nil, err
	}
	return &Gateway{
		baseURL:   strings.TrimRight(cfg.BaseURL, "/"),
		snapURL:   strings.TrimRight(snapBase(cfg), "/"),
		auth:      "Basic " + base64.StdEncoding.EncodeToString([]byte(cfg.ServerKey+":")),
		notifyURL: cfg.NotificationURL,
		client:    client, charge: cg, status: sg,
	}, nil
}

func snapBase(cfg Config) string {
	if cfg.SnapBaseURL != "" {
		return cfg.SnapBaseURL
	}
	return strings.Replace(cfg.BaseURL, "://api.", "://app.", 1)
}

func newGuard(name string, l Limits, now func() time.Time) (guard, error) {
	b, err := resilience.NewBreaker(resilience.BreakerConfig{
		Name: name, FailureThreshold: l.FailureThreshold, Cooldown: l.Cooldown, HalfOpenProbes: 1, Timeout: l.Timeout,
		IsFailure:     isProviderFailure,
		OnStateChange: telemetry.BreakerObserver(),
	}, now)
	if err != nil {
		return guard{}, err
	}
	bh, err := resilience.NewBulkhead(l.Bulkhead, 0)
	if err != nil {
		return guard{}, err
	}
	return guard{breaker: b, bulkhead: bh}, nil
}

// isProviderFailure counts only errors that say Midtrans is unhealthy: unreachable or ambiguous. A refusal (4xx),
// an unknown order, a full bulkhead, and cancellation are not provider failures (R9.2).
func isProviderFailure(err error) bool {
	return errors.Is(err, outbound.ErrGatewayAmbiguous) || errors.Is(err, outbound.ErrGatewayUnavailable)
}

// do runs call through the guard and maps breaker/bulkhead rejections to ErrGatewayUnavailable (nothing was sent).
func (g guard) do(ctx context.Context, call func(context.Context) error) error {
	err := g.breaker.Do(ctx, func(ctx context.Context) error {
		return g.bulkhead.Do(ctx, call)
	})
	if errors.Is(err, resilience.ErrOpen) || errors.Is(err, resilience.ErrBulkheadFull) {
		return fmt.Errorf("%w: %w", outbound.ErrGatewayUnavailable, err)
	}
	return err
}

// ---- snap ----

type snapBody struct {
	TransactionDetails txDetails     `json:"transaction_details"`
	Expiry             snapExpiry    `json:"expiry"`
	EnabledPayments    []string      `json:"enabled_payments,omitempty"`
	Callbacks          *snapCallback `json:"callbacks,omitempty"`
}
type txDetails struct {
	OrderID     string `json:"order_id"`
	GrossAmount int64  `json:"gross_amount"`
}
type snapExpiry struct {
	StartTime string `json:"start_time"`
	Unit      string `json:"unit"`
	Duration  int64  `json:"duration"`
}
type snapCallback struct {
	Finish string `json:"finish"`
}
type snapResponse struct {
	responseHeader
	Token       string `json:"token"`
	RedirectURL string `json:"redirect_url"`
}

// enabledPayments maps a payment-page method to the Snap channels offered to the payer (nil = all channels).
func enabledPayments(m vo.Method) ([]string, error) {
	switch m {
	case vo.MethodVABCA:
		return []string{"bca_va"}, nil
	case vo.MethodVABNI:
		return []string{"bni_va"}, nil
	case vo.MethodVABRI:
		return []string{"bri_va"}, nil
	case vo.MethodVAPermata:
		return []string{"permata_va"}, nil
	case vo.MethodQRIS:
		return []string{"qris"}, nil
	case vo.MethodGoPay:
		return []string{"gopay"}, nil
	case vo.MethodSnap:
		return nil, nil
	}
	return nil, fmt.Errorf("%w: metode %q", outbound.ErrGatewayRejected, m)
}

// Charge creates a Midtrans Snap token (POST /snap/v1/transactions). Amount and order id come from the backend only.
// Snap expiry is in whole minutes, rounded down so the transaction never outlives the booking. Status and cancel
// use the Core API order endpoints, which work for Snap orders.
func (g *Gateway) Charge(ctx context.Context, req outbound.ChargeRequest) (outbound.ChargeResult, error) {
	channels, err := enabledPayments(req.Method)
	if err != nil {
		return outbound.ChargeResult{}, err
	}
	minutes := int64(req.Expiry / time.Minute)
	if minutes < 1 {
		return outbound.ChargeResult{}, fmt.Errorf("%w: sisa waktu pembayaran kurang dari 1 menit", outbound.ErrGatewayRejected)
	}
	body := snapBody{
		TransactionDetails: txDetails{OrderID: req.OrderID, GrossAmount: req.AmountMinor},
		Expiry:             snapExpiry{StartTime: req.OrderTime.In(vo.WIB()).Format("2006-01-02 15:04:05 -0700"), Unit: "minute", Duration: minutes},
		EnabledPayments:    channels,
	}
	if req.FinishURL != "" {
		body.Callbacks = &snapCallback{Finish: req.FinishURL}
	}
	var resp snapResponse
	if err := g.charge.do(ctx, func(ctx context.Context) error {
		return g.callURL(ctx, g.snapURL+"/snap/v1/transactions", http.MethodPost, body, &resp, nil, g.overrideNotification)
	}); err != nil {
		return outbound.ChargeResult{}, err
	}
	if resp.Token == "" || resp.RedirectURL == "" {
		return outbound.ChargeResult{}, fmt.Errorf("%w: respons tanpa token Snap", outbound.ErrGatewayAmbiguous)
	}
	return outbound.ChargeResult{Instruction: entity.Instruction{Type: "SNAP", SnapToken: resp.Token, RedirectURL: resp.RedirectURL}}, nil
}

// ---- status and cancel ----

type statusResponse struct {
	responseHeader
	TransactionID     string `json:"transaction_id"`
	TransactionStatus string `json:"transaction_status"`
	FraudStatus       string `json:"fraud_status"`
	SettlementTime    string `json:"settlement_time"`
}

// Status reads the provider's current view of one order.
func (g *Gateway) Status(ctx context.Context, orderID string) (outbound.GatewayStatus, error) {
	var resp statusResponse
	if err := g.status.do(ctx, func(ctx context.Context) error {
		// An expired transaction answers with status_code 407 (deny: 202) while still carrying its transaction_status,
		// so a 4xx in the body is not a refusal when the status is present.
		return g.call(ctx, http.MethodGet, "/v2/"+url.PathEscape(orderID)+"/status", nil, &resp, func() bool { return resp.TransactionStatus != "" })

	}); err != nil {
		return outbound.GatewayStatus{}, err
	}
	st, ok := vo.MapProviderStatus(resp.TransactionStatus, resp.FraudStatus)
	if !ok {
		return outbound.GatewayStatus{}, fmt.Errorf("%w: status %q tidak dikenal", outbound.ErrGatewayAmbiguous, resp.TransactionStatus)
	}
	out := outbound.GatewayStatus{Status: st, ProviderTxID: resp.TransactionID, RawStatus: resp.TransactionStatus}
	if st == vo.AttemptSucceeded && resp.SettlementTime != "" {
		t, err := vo.ParseSettlementTime(resp.SettlementTime)
		if err != nil {
			return outbound.GatewayStatus{}, fmt.Errorf("%w: %w", outbound.ErrGatewayAmbiguous, err)
		}
		out.SettlementTime = &t
	}
	return out, nil
}

// Cancel cancels a pending order. A refusal (e.g. already settled) is ErrGatewayRejected: the caller must
// check the status again instead of assuming the order is cancelled.
func (g *Gateway) Cancel(ctx context.Context, orderID string) error {
	return g.status.do(ctx, func(ctx context.Context) error {
		return g.call(ctx, http.MethodPost, "/v2/"+url.PathEscape(orderID)+"/cancel", nil, &responseHeader{}, nil)
	})
}

// ---- transport ----

// responseHeader is the part every Midtrans response shares. status_code in the body is authoritative:
// Midtrans can answer HTTP 200 with a body status_code of 404.
type responseHeader struct {
	StatusCode    string `json:"status_code"`
	StatusMessage string `json:"status_message"`
}

func (h *responseHeader) header() *responseHeader { return h }

type withHeader interface{ header() *responseHeader }

// overrideNotification adds X-Override-Notification when a notification URL is configured.
func (g *Gateway) overrideNotification(req *http.Request) {
	if g.notifyURL != "" {
		req.Header.Set("X-Override-Notification", g.notifyURL)
	}
}

func (g *Gateway) call(ctx context.Context, method, path string, in any, out withHeader, tolerate4xx func() bool, opts ...func(*http.Request)) error {
	return g.callURL(ctx, g.baseURL+path, method, in, out, tolerate4xx, opts...)
}

func (g *Gateway) callURL(ctx context.Context, fullURL, method string, in any, out withHeader, tolerate4xx func() bool, opts ...func(*http.Request)) error {
	var body io.Reader
	if in != nil {
		raw, err := json.Marshal(in)
		if err != nil {
			return fmt.Errorf("%w: encode: %w", outbound.ErrGatewayRejected, err)
		}
		body = bytes.NewReader(raw)
	}
	req, err := http.NewRequestWithContext(ctx, method, fullURL, body)
	if err != nil {
		return fmt.Errorf("%w: %w", outbound.ErrGatewayRejected, err)
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Authorization", g.auth)
	if in != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	for _, opt := range opts {
		opt(req)
	}

	resp, err := g.client.Do(req)
	if err != nil {
		return classifyTransport(err)
	}
	defer func() { _ = resp.Body.Close() }()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, maxResponseBytes))
	if err != nil {
		return fmt.Errorf("%w: baca respons", outbound.ErrGatewayAmbiguous)
	}
	if resp.StatusCode >= 500 {
		return fmt.Errorf("%w: http %d", outbound.ErrGatewayAmbiguous, resp.StatusCode)
	}
	if resp.StatusCode == http.StatusTooManyRequests {
		return fmt.Errorf("%w: dibatasi (429)", outbound.ErrGatewayUnavailable)
	}
	// Decode errors are ignored on 4xx: the status code alone decides there.
	decodeErr := json.Unmarshal(raw, out)
	hdr := out.header()
	code := hdr.StatusCode
	if code == "" {
		code = fmt.Sprint(resp.StatusCode)
	}
	switch {
	case code == "404":
		return outbound.ErrOrderNotFound
	case tolerate4xx != nil && strings.HasPrefix(code, "4") && decodeErr == nil && tolerate4xx():
		return nil
	case strings.HasPrefix(code, "5"):
		return fmt.Errorf("%w: status %s", outbound.ErrGatewayAmbiguous, code)
	case strings.HasPrefix(code, "4"):
		return fmt.Errorf("%w: status %s: %s", outbound.ErrGatewayRejected, code, safeMessage(hdr.StatusMessage))
	case decodeErr != nil:
		return fmt.Errorf("%w: respons tidak terbaca", outbound.ErrGatewayAmbiguous)
	}
	return nil
}

// classifyTransport tells "never sent" (dial failure) from "maybe sent" (timeout, reset, EOF).
func classifyTransport(err error) error {
	if errors.Is(err, context.Canceled) {
		return err
	}
	var op *net.OpError
	if errors.As(err, &op) && op.Op == "dial" && !errors.Is(err, context.DeadlineExceeded) && !op.Timeout() {
		return fmt.Errorf("%w: koneksi gagal", outbound.ErrGatewayUnavailable)
	}
	return fmt.Errorf("%w: %s", outbound.ErrGatewayAmbiguous, transportReason(err))
}

func transportReason(err error) string {
	switch {
	case errors.Is(err, context.DeadlineExceeded):
		return "timeout"
	case errors.Is(err, io.EOF), errors.Is(err, io.ErrUnexpectedEOF):
		return "koneksi terputus"
	}
	var ne net.Error
	if errors.As(err, &ne) && ne.Timeout() {
		return "timeout"
	}
	return "kesalahan jaringan"
}

// safeMessage keeps a short provider message for logs. It never contains the server key (not part of any response).
func safeMessage(s string) string {
	if len(s) > 120 {
		return s[:120]
	}
	return s
}
