// Package midtrans implements IPaymentGateway on the Midtrans Core API (POST /v2/charge, GET /v2/{id}/status,
// POST /v2/{id}/cancel). Request and response shapes were checked against docs.midtrans.com on 2026-10-26;
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
	BaseURL   string
	ServerKey string
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
		auth:      "Basic " + base64.StdEncoding.EncodeToString([]byte(cfg.ServerKey+":")),
		notifyURL: cfg.NotificationURL,
		client:    client, charge: cg, status: sg,
	}, nil
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

// ---- charge ----

type chargeBody struct {
	PaymentType        string        `json:"payment_type"`
	TransactionDetails txDetails     `json:"transaction_details"`
	BankTransfer       *bankTransfer `json:"bank_transfer,omitempty"`
	Qris               *qris         `json:"qris,omitempty"`
	CustomExpiry       customExpiry  `json:"custom_expiry"`
}

type txDetails struct {
	OrderID     string `json:"order_id"`
	GrossAmount int64  `json:"gross_amount"`
}
type bankTransfer struct {
	Bank string `json:"bank"`
}
type qris struct {
	Acquirer string `json:"acquirer"`
}
type customExpiry struct {
	OrderTime      string `json:"order_time"`
	ExpiryDuration int64  `json:"expiry_duration"`
	Unit           string `json:"unit"`
}

// Charge asks Midtrans for a payment instruction. Gross amount is amountMinor as whole rupiah (IDR has no decimals).
func (g *Gateway) Charge(ctx context.Context, req outbound.ChargeRequest) (outbound.ChargeResult, error) {
	body, err := buildCharge(req)
	if err != nil {
		return outbound.ChargeResult{}, err
	}
	var resp chargeResponse
	if err := g.charge.do(ctx, func(ctx context.Context) error {
		return g.call(ctx, http.MethodPost, "/v2/charge", body, &resp, nil, g.overrideNotification)
	}); err != nil {
		return outbound.ChargeResult{}, err
	}
	ins, err := instructionFrom(req.Method, resp)
	if err != nil {
		return outbound.ChargeResult{}, err
	}
	return outbound.ChargeResult{ProviderTxID: resp.TransactionID, Instruction: ins}, nil
}

func buildCharge(req outbound.ChargeRequest) (chargeBody, error) {
	b := chargeBody{
		TransactionDetails: txDetails{OrderID: req.OrderID, GrossAmount: req.AmountMinor},
		CustomExpiry: customExpiry{
			OrderTime:      req.OrderTime.In(vo.WIB()).Format("2006-01-02 15:04:05 -0700"),
			ExpiryDuration: int64(req.Expiry / time.Second),
			Unit:           "second",
		},
	}
	switch req.Method {
	case vo.MethodVABCA:
		b.PaymentType, b.BankTransfer = "bank_transfer", &bankTransfer{Bank: "bca"}
	case vo.MethodVABNI:
		b.PaymentType, b.BankTransfer = "bank_transfer", &bankTransfer{Bank: "bni"}
	case vo.MethodVABRI:
		b.PaymentType, b.BankTransfer = "bank_transfer", &bankTransfer{Bank: "bri"}
	case vo.MethodVAPermata:
		b.PaymentType = "permata"
	case vo.MethodQRIS:
		b.PaymentType, b.Qris = "qris", &qris{Acquirer: "gopay"}
	case vo.MethodGoPay:
		b.PaymentType = "gopay"
	default:
		return chargeBody{}, fmt.Errorf("%w: metode %q", outbound.ErrGatewayRejected, req.Method)
	}
	if req.Expiry < time.Second {
		return chargeBody{}, fmt.Errorf("%w: sisa waktu pembayaran habis", outbound.ErrGatewayRejected)
	}
	return b, nil
}

type action struct {
	Name string `json:"name"`
	URL  string `json:"url"`
}

type chargeResponse struct {
	responseHeader
	TransactionID   string     `json:"transaction_id"`
	VANumbers       []vaNumber `json:"va_numbers"`
	PermataVANumber string     `json:"permata_va_number"`
	Actions         []action   `json:"actions"`
}

type vaNumber struct {
	Bank     string `json:"bank"`
	VANumber string `json:"va_number"`
}

func (r chargeResponse) actionURL(name string) string {
	for _, a := range r.Actions {
		if a.Name == name {
			return a.URL
		}
	}
	return ""
}

func instructionFrom(m vo.Method, r chargeResponse) (entity.Instruction, error) {
	switch m {
	case vo.MethodVABCA, vo.MethodVABNI, vo.MethodVABRI:
		if len(r.VANumbers) == 0 || r.VANumbers[0].VANumber == "" {
			return entity.Instruction{}, fmt.Errorf("%w: respons tanpa nomor VA", outbound.ErrGatewayAmbiguous)
		}
		return entity.Instruction{Type: "VA", Bank: r.VANumbers[0].Bank, VANumber: r.VANumbers[0].VANumber}, nil
	case vo.MethodVAPermata:
		if r.PermataVANumber == "" {
			return entity.Instruction{}, fmt.Errorf("%w: respons tanpa nomor VA", outbound.ErrGatewayAmbiguous)
		}
		return entity.Instruction{Type: "VA", Bank: "permata", VANumber: r.PermataVANumber}, nil
	case vo.MethodQRIS:
		qr := r.actionURL("generate-qr-code")
		if qr == "" {
			return entity.Instruction{}, fmt.Errorf("%w: respons tanpa QR", outbound.ErrGatewayAmbiguous)
		}
		return entity.Instruction{Type: "QR", QRURL: qr}, nil
	case vo.MethodGoPay:
		ins := entity.Instruction{Type: "DEEPLINK", DeeplinkURL: r.actionURL("deeplink-redirect"), QRURL: r.actionURL("generate-qr-code")}
		if ins.DeeplinkURL == "" && ins.QRURL == "" {
			return entity.Instruction{}, fmt.Errorf("%w: respons tanpa tautan GoPay", outbound.ErrGatewayAmbiguous)
		}
		return ins, nil
	}
	return entity.Instruction{}, fmt.Errorf("%w: metode %q", outbound.ErrGatewayRejected, m)
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
	var body io.Reader
	if in != nil {
		raw, err := json.Marshal(in)
		if err != nil {
			return fmt.Errorf("%w: encode: %w", outbound.ErrGatewayRejected, err)
		}
		body = bytes.NewReader(raw)
	}
	req, err := http.NewRequestWithContext(ctx, method, g.baseURL+path, body)
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
