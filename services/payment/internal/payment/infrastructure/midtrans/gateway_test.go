package midtrans_test

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tiket-sampai-nonton/rusuh-booking-be/pkg/resilience"

	vo "github.com/tiket-sampai-nonton/rusuh-booking-be/services/payment/internal/payment/domain/valueobject"
	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/payment/internal/payment/infrastructure/midtrans"
	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/payment/internal/payment/port/outbound"
)

const serverKey = "SB-Mid-server-SECRET-KEY"

var limits = midtrans.Limits{Timeout: 500 * time.Millisecond, FailureThreshold: 3, Cooldown: time.Minute, Bulkhead: 5}

func newGateway(t *testing.T, h http.Handler) (*midtrans.Gateway, *httptest.Server) {
	t.Helper()
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	g, err := midtrans.New(midtrans.Config{BaseURL: srv.URL, ServerKey: serverKey, Charge: limits, Status: limits}, time.Now)
	require.NoError(t, err)
	return g, srv
}

func chargeReq(m vo.Method) outbound.ChargeRequest {
	return outbound.ChargeRequest{
		OrderID: "RB-7F3A9C21-1", Method: m, AmountMinor: 1500000,
		OrderTime: time.Date(2026, 10, 26, 10, 0, 0, 0, time.UTC), Expiry: 9*time.Minute + 30*time.Second + 400*time.Millisecond,
	}
}

func reply(w http.ResponseWriter, httpStatus int, body string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(httpStatus)
	_, _ = w.Write([]byte(body))
}

// capture returns a handler that records the request and replies with body.
func capture(got *map[string]any, hdr *http.Header, path *string, body string) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		*hdr, *path = r.Header.Clone(), r.Method+" "+r.URL.Path
		if r.Body != nil {
			_ = json.NewDecoder(r.Body).Decode(got)
		}
		reply(w, 200, body)
	})
}

func TestCharge_BuildsRequestPerMethodAndParsesInstruction(t *testing.T) {
	const vaResp = `{"status_code":"201","transaction_id":"tx-1","transaction_status":"pending","va_numbers":[{"bank":"bca","va_number":"812785002530231"}]}`
	const goResp = `{"status_code":"201","transaction_id":"tx-2","actions":[{"name":"generate-qr-code","url":"https://qr/png"},{"name":"deeplink-redirect","url":"https://gojek/pay"},{"name":"cancel","url":"https://x/cancel"}]}`
	cases := []struct {
		name   string
		method vo.Method
		resp   string
		check  func(t *testing.T, sent map[string]any)
		want   outbound.ChargeResult
	}{
		{"BCA", vo.MethodVABCA, vaResp, func(t *testing.T, s map[string]any) {
			assert.Equal(t, "bank_transfer", s["payment_type"])
			assert.Equal(t, map[string]any{"bank": "bca"}, s["bank_transfer"])
		}, outbound.ChargeResult{ProviderTxID: "tx-1"}},
		{"BNI", vo.MethodVABNI, strings.ReplaceAll(vaResp, "bca", "bni"), func(t *testing.T, s map[string]any) {
			assert.Equal(t, map[string]any{"bank": "bni"}, s["bank_transfer"])
		}, outbound.ChargeResult{ProviderTxID: "tx-1"}},
		{"BRI", vo.MethodVABRI, strings.ReplaceAll(vaResp, "bca", "bri"), func(t *testing.T, s map[string]any) {
			assert.Equal(t, map[string]any{"bank": "bri"}, s["bank_transfer"])
		}, outbound.ChargeResult{ProviderTxID: "tx-1"}},
		{"Permata", vo.MethodVAPermata, `{"status_code":"201","transaction_id":"tx-3","permata_va_number":"850003072869607"}`, func(t *testing.T, s map[string]any) {
			assert.Equal(t, "permata", s["payment_type"])
			assert.NotContains(t, s, "bank_transfer")
		}, outbound.ChargeResult{ProviderTxID: "tx-3"}},
		{"QRIS", vo.MethodQRIS, goResp, func(t *testing.T, s map[string]any) {
			assert.Equal(t, "qris", s["payment_type"])
			assert.Equal(t, map[string]any{"acquirer": "gopay"}, s["qris"])
		}, outbound.ChargeResult{ProviderTxID: "tx-2"}},
		{"GoPay", vo.MethodGoPay, goResp, func(t *testing.T, s map[string]any) {
			assert.Equal(t, "gopay", s["payment_type"])
		}, outbound.ChargeResult{ProviderTxID: "tx-2"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			var sent map[string]any
			var hdr http.Header
			var path string
			g, _ := newGateway(t, capture(&sent, &hdr, &path, c.resp))

			res, err := g.Charge(context.Background(), chargeReq(c.method))
			require.NoError(t, err)
			assert.Equal(t, c.want.ProviderTxID, res.ProviderTxID)
			c.check(t, sent)

			assert.Equal(t, "POST /v2/charge", path)
			assert.Equal(t, "Basic "+base64.StdEncoding.EncodeToString([]byte(serverKey+":")), hdr.Get("Authorization"))
			assert.Equal(t, map[string]any{"order_id": "RB-7F3A9C21-1", "gross_amount": float64(1500000)}, sent["transaction_details"])
			assert.Equal(t, map[string]any{"order_time": "2026-10-26 17:00:00 +0700", "expiry_duration": float64(570), "unit": "second"}, sent["custom_expiry"],
				"expiry dihitung dari sisa waktu booking (dibulatkan ke bawah), order_time WIB")
		})
	}
}

func TestCharge_Instructions(t *testing.T) {
	g, _ := newGateway(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var b map[string]any
		_ = json.NewDecoder(r.Body).Decode(&b)
		switch b["payment_type"] {
		case "bank_transfer":
			reply(w, 200, `{"status_code":"201","transaction_id":"t","va_numbers":[{"bank":"bca","va_number":"812785002530231"}]}`)
		case "permata":
			reply(w, 200, `{"status_code":"201","transaction_id":"t","permata_va_number":"850003072869607"}`)
		default:
			reply(w, 200, `{"status_code":"201","transaction_id":"t","actions":[{"name":"generate-qr-code","url":"https://qr/png"},{"name":"deeplink-redirect","url":"https://gojek/pay"}]}`)
		}
	}))
	ctx := context.Background()

	r, err := g.Charge(ctx, chargeReq(vo.MethodVABCA))
	require.NoError(t, err)
	assert.Equal(t, "VA", r.Instruction.Type)
	assert.Equal(t, "bca", r.Instruction.Bank)
	assert.Equal(t, "812785002530231", r.Instruction.VANumber)

	r, err = g.Charge(ctx, chargeReq(vo.MethodVAPermata))
	require.NoError(t, err)
	assert.Equal(t, "permata", r.Instruction.Bank)
	assert.Equal(t, "850003072869607", r.Instruction.VANumber)

	r, err = g.Charge(ctx, chargeReq(vo.MethodQRIS))
	require.NoError(t, err)
	assert.Equal(t, "QR", r.Instruction.Type)
	assert.Equal(t, "https://qr/png", r.Instruction.QRURL)

	r, err = g.Charge(ctx, chargeReq(vo.MethodGoPay))
	require.NoError(t, err)
	assert.Equal(t, "DEEPLINK", r.Instruction.Type)
	assert.Equal(t, "https://gojek/pay", r.Instruction.DeeplinkURL)
	assert.Equal(t, "https://qr/png", r.Instruction.QRURL, "web memakai QR, mobile memakai deeplink")
}

func TestCharge_IncompleteResponseIsAmbiguousNotSuccess(t *testing.T) {
	g, _ := newGateway(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		reply(w, 200, `{"status_code":"201","transaction_id":"t"}`)
	}))
	for _, m := range []vo.Method{vo.MethodVABCA, vo.MethodVAPermata, vo.MethodQRIS, vo.MethodGoPay} {
		_, err := g.Charge(context.Background(), chargeReq(m))
		assert.ErrorIs(t, err, outbound.ErrGatewayAmbiguous, m, "order sudah mungkin dibuat di Midtrans: jangan buat order baru")
	}
}

func TestCharge_ErrorClassification(t *testing.T) {
	cases := []struct {
		name     string
		httpCode int
		body     string
		want     error
	}{
		{"4xx HTTP", 400, `{"status_code":"400","status_message":"validation error"}`, outbound.ErrGatewayRejected},
		{"406 duplicate order", 200, `{"status_code":"406","status_message":"Duplicate order ID"}`, outbound.ErrGatewayRejected},
		{"401 key salah", 401, `{"status_code":"401","status_message":"Access denied"}`, outbound.ErrGatewayRejected},
		{"5xx HTTP", 502, ``, outbound.ErrGatewayAmbiguous},
		{"5xx body", 200, `{"status_code":"500","status_message":"internal"}`, outbound.ErrGatewayAmbiguous},
		{"429", 429, ``, outbound.ErrGatewayUnavailable},
		{"respons bukan JSON", 200, `<html>`, outbound.ErrGatewayAmbiguous},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			g, _ := newGateway(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { reply(w, c.httpCode, c.body) }))
			_, err := g.Charge(context.Background(), chargeReq(vo.MethodVABCA))
			assert.ErrorIs(t, err, c.want)
			assert.NotContains(t, err.Error(), serverKey)
		})
	}
}

func TestCharge_TimeoutIsAmbiguous(t *testing.T) {
	g, _ := newGateway(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-r.Context().Done():
		case <-time.After(3 * time.Second):
		}
	}))
	start := time.Now()
	_, err := g.Charge(context.Background(), chargeReq(vo.MethodVABCA))
	assert.ErrorIs(t, err, outbound.ErrGatewayAmbiguous, "Midtrans mungkin sudah membuat order")
	assert.Less(t, time.Since(start), 2*time.Second, "dibatasi timeout breaker")
}

func TestCharge_ConnectionRefusedIsUnavailable(t *testing.T) {
	srv := httptest.NewServer(http.NotFoundHandler())
	url := srv.URL
	srv.Close()
	g, err := midtrans.New(midtrans.Config{BaseURL: url, ServerKey: serverKey, Charge: limits, Status: limits}, time.Now)
	require.NoError(t, err)
	_, err = g.Charge(context.Background(), chargeReq(vo.MethodVABCA))
	assert.ErrorIs(t, err, outbound.ErrGatewayUnavailable, "tidak pernah terkirim: aman dicoba lagi")
}

func TestCharge_RejectsBadRequestsWithoutCalling(t *testing.T) {
	var calls atomic.Int32
	g, _ := newGateway(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { calls.Add(1); reply(w, 200, `{}`) }))
	_, err := g.Charge(context.Background(), outbound.ChargeRequest{OrderID: "x", Method: "CREDIT_CARD", AmountMinor: 1, Expiry: time.Minute})
	assert.ErrorIs(t, err, outbound.ErrGatewayRejected)
	req := chargeReq(vo.MethodVABCA)
	req.Expiry = 500 * time.Millisecond
	_, err = g.Charge(context.Background(), req)
	assert.ErrorIs(t, err, outbound.ErrGatewayRejected, "sisa waktu < 1 detik tidak bisa dibulatkan menjadi expiry")
	assert.EqualValues(t, 0, calls.Load())
}

// R9.2: penolakan bisnis tidak membuka breaker; kegagalan provider membukanya (R9.3).
func TestBreaker_BusinessRejectsDoNotOpenButProviderFailuresDo(t *testing.T) {
	var calls atomic.Int32
	mode := atomic.Value{}
	mode.Store("reject")
	g, _ := newGateway(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls.Add(1)
		if mode.Load() == "reject" {
			reply(w, 400, `{"status_code":"400","status_message":"validation error"}`)
			return
		}
		reply(w, 503, ``)
	}))
	ctx := context.Background()
	for i := 0; i < 10; i++ {
		_, err := g.Charge(ctx, chargeReq(vo.MethodVABCA))
		require.ErrorIs(t, err, outbound.ErrGatewayRejected)
	}
	assert.EqualValues(t, 10, calls.Load(), "10 penolakan 4xx tidak membuka breaker")

	mode.Store("down")
	for i := 0; i < 3; i++ { // ambang = 3
		_, err := g.Charge(ctx, chargeReq(vo.MethodVABCA))
		require.ErrorIs(t, err, outbound.ErrGatewayAmbiguous)
	}
	before := calls.Load()
	_, err := g.Charge(ctx, chargeReq(vo.MethodVABCA))
	require.ErrorIs(t, err, outbound.ErrGatewayUnavailable)
	assert.ErrorIs(t, err, resilience.ErrOpen)
	assert.Equal(t, before, calls.Load(), "breaker OPEN gagal cepat tanpa memanggil Midtrans")

	// Breaker status terpisah dari breaker charge.
	_, err = g.Status(ctx, "RB-1-1")
	assert.NotErrorIs(t, err, resilience.ErrOpen)
}

func TestStatus_MapsMidtransStatuses(t *testing.T) {
	cases := []struct {
		body       string
		want       vo.AttemptStatus
		settlement string
	}{
		{`{"status_code":"201","transaction_status":"pending","transaction_id":"t"}`, vo.AttemptPending, ""},
		{`{"status_code":"200","transaction_status":"settlement","transaction_id":"t","settlement_time":"2026-10-26 17:30:05","fraud_status":"accept"}`, vo.AttemptSucceeded, "2026-10-26T10:30:05Z"},
		{`{"status_code":"202","transaction_status":"deny","transaction_id":"t"}`, vo.AttemptFailed, ""},
		{`{"status_code":"200","transaction_status":"cancel","transaction_id":"t"}`, vo.AttemptCancelled, ""},
		{`{"status_code":"407","status_message":"Success, transaction is found","transaction_status":"expire","transaction_id":"t"}`, vo.AttemptExpired, ""},
	}
	for _, c := range cases {
		t.Run(string(c.want), func(t *testing.T) {
			var path string
			g, _ := newGateway(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				path = r.Method + " " + r.URL.Path
				reply(w, 200, c.body)
			}))
			st, err := g.Status(context.Background(), "RB-7F3A9C21-1")
			require.NoError(t, err)
			assert.Equal(t, "GET /v2/RB-7F3A9C21-1/status", path)
			assert.Equal(t, c.want, st.Status)
			if c.settlement == "" {
				assert.Nil(t, st.SettlementTime)
			} else {
				require.NotNil(t, st.SettlementTime)
				assert.Equal(t, c.settlement, st.SettlementTime.Format(time.RFC3339))
			}
		})
	}
}

func TestStatus_NotFoundAndUnknownStatus(t *testing.T) {
	g, _ := newGateway(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v2/missing-http/status":
			reply(w, 404, `{"status_code":"404","status_message":"Transaction doesn't exist."}`)
		case "/v2/missing-body/status":
			reply(w, 200, `{"status_code":"404","status_message":"Transaction doesn't exist."}`)
		default:
			reply(w, 200, `{"status_code":"200","transaction_status":"refund"}`)
		}
	}))
	for _, id := range []string{"missing-http", "missing-body"} {
		_, err := g.Status(context.Background(), id)
		assert.ErrorIs(t, err, outbound.ErrOrderNotFound, id)
	}
	_, err := g.Status(context.Background(), "refunded")
	assert.ErrorIs(t, err, outbound.ErrGatewayAmbiguous, "status tanpa makna pembayaran tidak dianggap hasil")
}

func TestCancel(t *testing.T) {
	var path string
	g, _ := newGateway(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path = r.Method + " " + r.URL.Path
		switch r.URL.Path {
		case "/v2/ok/cancel":
			reply(w, 200, `{"status_code":"200","transaction_status":"cancel"}`)
		case "/v2/settled/cancel":
			reply(w, 200, `{"status_code":"412","status_message":"Transaction status cannot be updated."}`)
		default:
			reply(w, 404, `{"status_code":"404"}`)
		}
	}))
	require.NoError(t, g.Cancel(context.Background(), "ok"))
	assert.Equal(t, "POST /v2/ok/cancel", path)
	assert.ErrorIs(t, g.Cancel(context.Background(), "settled"), outbound.ErrGatewayRejected, "tidak bisa dibatalkan → cek status lagi")
	assert.ErrorIs(t, g.Cancel(context.Background(), "nope"), outbound.ErrOrderNotFound)
}

func TestNew_ValidatesConfigAndHidesKey(t *testing.T) {
	_, err := midtrans.New(midtrans.Config{BaseURL: "ftp://x", ServerKey: serverKey, Charge: limits, Status: limits}, time.Now)
	assert.Error(t, err)
	_, err = midtrans.New(midtrans.Config{BaseURL: "https://api.sandbox.midtrans.com", ServerKey: "", Charge: limits, Status: limits}, time.Now)
	assert.Error(t, err)
	_, err = midtrans.New(midtrans.Config{BaseURL: "https://api.sandbox.midtrans.com", ServerKey: serverKey, Charge: midtrans.Limits{}, Status: limits}, time.Now)
	assert.Error(t, err)
	assert.False(t, errors.Is(err, outbound.ErrGatewayRejected))
}

func TestRedirectIsNotFollowed(t *testing.T) {
	var hit atomic.Int32
	other := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { hit.Add(1) }))
	defer other.Close()
	g, _ := newGateway(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, other.URL, http.StatusTemporaryRedirect)
	}))
	_, err := g.Charge(context.Background(), chargeReq(vo.MethodVABCA))
	assert.Error(t, err)
	assert.EqualValues(t, 0, hit.Load(), "header Authorization tidak ikut ke host lain")
}

func TestCharge_OverridesNotificationURLOnlyWhenConfigured(t *testing.T) {
	var header string
	var hasHeader bool
	h := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		header, hasHeader = r.Header.Get("X-Override-Notification"), r.Header.Get("X-Override-Notification") != ""
		reply(w, 200, `{"status_code":"201","transaction_id":"t","va_numbers":[{"bank":"bca","va_number":"1"}]}`)
	})
	srv := httptest.NewServer(h)
	defer srv.Close()

	g, err := midtrans.New(midtrans.Config{BaseURL: srv.URL, ServerKey: serverKey, NotificationURL: "https://abc.example.dev/api/v1/webhooks/midtrans", Charge: limits, Status: limits}, time.Now)
	require.NoError(t, err)
	_, err = g.Charge(context.Background(), chargeReq(vo.MethodVABCA))
	require.NoError(t, err)
	assert.Equal(t, "https://abc.example.dev/api/v1/webhooks/midtrans", header)

	g, err = midtrans.New(midtrans.Config{BaseURL: srv.URL, ServerKey: serverKey, Charge: limits, Status: limits}, time.Now)
	require.NoError(t, err)
	_, err = g.Charge(context.Background(), chargeReq(vo.MethodVABCA))
	require.NoError(t, err)
	assert.False(t, hasHeader, "tanpa konfigurasi, dashboard yang menentukan")
}
