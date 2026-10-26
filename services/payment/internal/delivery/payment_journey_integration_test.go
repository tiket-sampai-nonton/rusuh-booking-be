//go:build integration

package delivery

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha512"
	"crypto/x509"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tiket-sampai-nonton/rusuh-booking-be/pkg/contract"
	"github.com/tiket-sampai-nonton/rusuh-booking-be/pkg/migrate"
	"github.com/tiket-sampai-nonton/rusuh-booking-be/pkg/postgres"
	"github.com/tiket-sampai-nonton/rusuh-booking-be/pkg/rabbitmq"
	"github.com/tiket-sampai-nonton/rusuh-booking-be/pkg/token"

	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/payment/deployments/migrations"
)

const (
	journeySchema = "it_payment_journey"
	serverKey     = "SB-Mid-server-JOURNEY-KEY"
	visitorID     = "22222222-2222-2222-2222-222222222222"
	bookingA      = "11111111-1111-1111-1111-111111111111"
	bookingB      = "33333333-3333-3333-3333-333333333333"
	orderA        = "RB-7F3A9C21-1"

	// eventContracts is docs/contracts/events seen from this package.
	eventContracts = "../../../../docs/contracts/events/"
)

var quiet = slog.New(slog.NewTextHandler(io.Discard, nil))

type keyPair struct {
	pub    ed25519.PublicKey
	priv   ed25519.PrivateKey
	signer *token.Signer
}

func newKeyPair(t *testing.T, kid string) keyPair {
	t.Helper()
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	require.NoError(t, err)
	s, err := token.NewSigner(priv, kid)
	require.NoError(t, err)
	return keyPair{pub: pub, priv: priv, signer: s}
}

func writePEM(t *testing.T, name string, k keyPair, private bool) string {
	t.Helper()
	var block *pem.Block
	if private {
		der, err := x509.MarshalPKCS8PrivateKey(k.priv)
		require.NoError(t, err)
		block = &pem.Block{Type: "PRIVATE KEY", Bytes: der}
	} else {
		der, err := x509.MarshalPKIXPublicKey(k.pub)
		require.NoError(t, err)
		block = &pem.Block{Type: "PUBLIC KEY", Bytes: der}
	}
	path := filepath.Join(t.TempDir(), name)
	require.NoError(t, os.WriteFile(path, pem.EncodeToMemory(block), 0o600))
	return path
}

// fakeBooking answers Payment's internal calls and checks the service token it receives.
type fakeBooking struct {
	srv   *httptest.Server
	locks atomic.Int32
}

func newFakeBooking(t *testing.T, paymentPub ed25519.PublicKey) *fakeBooking {
	t.Helper()
	verifier, err := token.NewVerifier(map[string]ed25519.PublicKey{"payment-1": paymentPub}, "rbt-payment", "rbt-booking", 5*time.Second)
	require.NoError(t, err)
	fb := &fakeBooking{}
	mux := http.NewServeMux()
	auth := func(h http.HandlerFunc) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) {
			tok := r.Header.Get("Authorization")
			if len(tok) < 8 {
				w.WriteHeader(http.StatusUnauthorized)
				return
			}
			if _, err := verifier.Verify(tok[7:], time.Now()); err != nil {
				w.WriteHeader(http.StatusUnauthorized)
				return
			}
			h(w, r)
		}
	}
	reply := func(w http.ResponseWriter, data any) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"meta": map[string]any{"code": 200}, "data": data})
	}
	mux.HandleFunc("GET /internal/v1/bookings/{id}/payable", auth(func(w http.ResponseWriter, r *http.Request) {
		id := r.PathValue("id")
		if id != bookingA && id != bookingB {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		ref := "RB-7F3A9C21"
		if id == bookingB {
			ref = "RB-0B0B0B0B"
		}
		reply(w, map[string]any{
			"bookingId": id, "publicRef": ref, "status": "PENDING_PAYMENT", "amountMinor": 1500000,
			"paymentExpiresAt": time.Now().Add(8 * time.Minute).UTC().Format(time.RFC3339), "visitorId": visitorID, "version": 3, "canEdit": false,
		})
	}))
	mux.HandleFunc("POST /internal/v1/bookings/{id}/payment-lock", auth(func(w http.ResponseWriter, r *http.Request) {
		fb.locks.Add(1)
		reply(w, map[string]any{
			"outcome": "OK", "version": 4, "amountMinor": 1500000,
			"paymentExpiresAt": time.Now().Add(8 * time.Minute).UTC().Format(time.RFC3339),
		})
	}))
	fb.srv = httptest.NewServer(mux)
	t.Cleanup(fb.srv.Close)
	return fb
}

// fakeMidtrans records charges and reports a settlement once paid is set.
type fakeMidtrans struct {
	srv     *httptest.Server
	charges atomic.Int32
	paid    atomic.Bool
	lastReq atomic.Value // map[string]any
	auth    atomic.Value // string
}

func newFakeMidtrans(t *testing.T) *fakeMidtrans {
	t.Helper()
	fm := &fakeMidtrans{}
	mux := http.NewServeMux()
	mux.HandleFunc("POST /v2/charge", func(w http.ResponseWriter, r *http.Request) {
		fm.charges.Add(1)
		fm.auth.Store(r.Header.Get("Authorization"))
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		fm.lastReq.Store(body)
		_, _ = w.Write([]byte(`{"status_code":"201","transaction_id":"tx-journey","transaction_status":"pending","va_numbers":[{"bank":"bca","va_number":"812785002530231"}]}`))
	})
	mux.HandleFunc("GET /v2/{id}/status", func(w http.ResponseWriter, r *http.Request) {
		if fm.paid.Load() {
			_, _ = w.Write([]byte(`{"status_code":"200","transaction_id":"tx-journey","transaction_status":"settlement","settlement_time":"2026-10-26 17:30:05"}`))
			return
		}
		_, _ = w.Write([]byte(`{"status_code":"201","transaction_id":"tx-journey","transaction_status":"pending"}`))
	})
	mux.HandleFunc("POST /v2/{id}/cancel", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"status_code":"200","transaction_status":"cancel"}`))
	})
	fm.srv = httptest.NewServer(mux)
	t.Cleanup(fm.srv.Close)
	return fm
}

type journey struct {
	t        *testing.T
	api      *httptest.Server
	pool     *pgxpool.Pool
	visitor  string
	service  string
	midtrans *fakeMidtrans
	booking  *fakeBooking
	clients  *Clients
}

func (j *journey) call(method, path, body string, hdr map[string]string) (int, http.Header, []byte) {
	j.t.Helper()
	req, err := http.NewRequest(method, j.api.URL+path, bytes.NewBufferString(body))
	require.NoError(j.t, err)
	req.Header.Set("Content-Type", "application/json")
	for k, v := range hdr {
		req.Header.Set(k, v)
	}
	resp, err := (&http.Client{Timeout: 10 * time.Second}).Do(req)
	require.NoError(j.t, err)
	defer func() { _ = resp.Body.Close() }()
	raw, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, resp.Header, raw
}

func (j *journey) asVisitor() map[string]string {
	return map[string]string{"X-Visitor-Token": j.visitor}
}

func (j *journey) count(q string) int {
	j.t.Helper()
	var n int
	require.NoError(j.t, j.pool.QueryRow(context.Background(), q).Scan(&n))
	return n
}

func data[T any](t *testing.T, raw []byte) T {
	t.Helper()
	var env struct {
		Data T `json:"data"`
	}
	require.NoError(t, json.Unmarshal(raw, &env), string(raw))
	return env.Data
}

func setupJourney(t *testing.T) *journey {
	t.Helper()
	dsn := os.Getenv("TEST_POSTGRES_DSN")
	rmq := os.Getenv("TEST_RABBITMQ_URL")
	if dsn == "" || rmq == "" {
		t.Skip("TEST_POSTGRES_DSN dan TEST_RABBITMQ_URL wajib diset")
	}
	ctx := context.Background()
	admin, err := pgxpool.New(ctx, dsn)
	require.NoError(t, err)
	_, err = admin.Exec(ctx, `DROP SCHEMA IF EXISTS `+journeySchema+` CASCADE; CREATE SCHEMA `+journeySchema)
	require.NoError(t, err)
	admin.Close()

	queueKey, paymentKey, bookingKey := newKeyPair(t, "queue-1"), newKeyPair(t, "payment-1"), newKeyPair(t, "booking-1")
	fb := newFakeBooking(t, paymentKey.pub)
	fm := newFakeMidtrans(t)

	cfg := testConfig(t)
	port, _ := strconv.Atoi(os.Getenv("TEST_POSTGRES_PORT"))
	cfg.Postgres = postgres.Config{
		Host: os.Getenv("TEST_POSTGRES_HOST"), Port: port, User: os.Getenv("TEST_POSTGRES_USER"), Password: os.Getenv("TEST_POSTGRES_PASSWORD"),
		Database: os.Getenv("TEST_POSTGRES_DATABASE"), SSLMode: "disable", Schema: journeySchema, MaxConns: 10,
	}
	cfg.RabbitMQ = rabbitmq.Config{URL: rmq, Exchange: "rusuh.it.payment", ExchangeType: "topic", Workers: 1, Prefetch: 5,
		MaxRetries: 1, RetryDelays: []time.Duration{time.Second}, ConfirmWait: 5 * time.Second}
	cfg.App.Env = "local"
	cfg.Payment.Midtrans.BaseURL = fm.srv.URL
	cfg.Payment.Midtrans.ServerKey = serverKey
	cfg.Payment.BookingURL = fb.srv.URL
	cfg.Payment.Token.VisitorPublicKeyFile = writePEM(t, "queue.pub", queueKey, false)
	cfg.Payment.Token.ServicePrivateKeyFile = writePEM(t, "payment.key", paymentKey, true)
	cfg.Payment.Token.BookingPublicKeyFile = writePEM(t, "booking.pub", bookingKey, false)
	require.NoError(t, cfg.Validate())

	clients, err := NewClients(ctx, cfg, Need{Postgres: true, RabbitMQ: true})
	require.NoError(t, err)
	t.Cleanup(func() {
		_, _ = clients.PG.Pool().Exec(context.Background(), `DROP SCHEMA IF EXISTS `+journeySchema+` CASCADE`)
		clients.Close()
	})
	migs, err := migrate.Load(migrations.FS, ".")
	require.NoError(t, err)
	runner, err := migrate.NewRunner(clients.PG.Pool(), journeySchema, migs, quiet)
	require.NoError(t, err)
	_, err = runner.Up(ctx, 0)
	require.NoError(t, err)

	h, err := NewAPIHandler(cfg, clients, quiet)
	require.NoError(t, err)
	api := httptest.NewServer(h)
	t.Cleanup(api.Close)

	sign := func(k keyPair, c token.Claims) string {
		tok, err := k.signer.Sign(c)
		require.NoError(t, err)
		return tok
	}
	exp := time.Now().Add(time.Hour).Unix()
	return &journey{
		t: t, api: api, pool: clients.PG.Pool(), midtrans: fm, booking: fb, clients: clients,
		visitor: sign(queueKey, token.Claims{Issuer: "rbt-queue", Subject: visitorID, Audience: "rbt", IssuedAt: time.Now().Unix(), ExpiresAt: exp}),
		service: sign(bookingKey, token.Claims{Issuer: "rbt-booking", Subject: "rbt-booking", Audience: "rbt-payment", IssuedAt: time.Now().Unix(), ExpiresAt: time.Now().Add(5 * time.Minute).Unix()}),
	}
}

func signature(order, code, gross string) string {
	sum := sha512.Sum512([]byte(order + code + gross + serverKey))
	return hex.EncodeToString(sum[:])
}

func notification(status, sig string) string {
	return fmt.Sprintf(`{"order_id":%q,"transaction_status":%q,"status_code":"200","gross_amount":"1500000.00","signature_key":%q,
		"transaction_id":"tx-journey","settlement_time":"2026-10-26 17:30:05","fraud_status":"accept","payment_type":"bank_transfer"}`, orderA, status, sig)
}

// TestPaymentJourney walks the whole Payment flow against real PostgreSQL and RabbitMQ, with Midtrans and Booking
// replaced by local fakes: page → attempt → idempotent retry → bad and good webhooks → outbox → RabbitMQ →
// resolve → WebView page token.
func TestPaymentJourney(t *testing.T) {
	j := setupJourney(t)
	ctx := context.Background()
	pageHdr := j.asVisitor()

	// FR-20/21: payment page shows amount, deadline, and the enabled methods.
	code, _, raw := j.call(http.MethodGet, "/api/v1/payments/"+bookingA+"/page", "", pageHdr)
	require.Equal(t, 200, code, string(raw))
	page := data[map[string]any](t, raw)
	assert.EqualValues(t, 1500000, page["amountMinor"])
	assert.Contains(t, page["methods"], "VA_BCA")
	assert.Nil(t, page["currentAttempt"])

	// Someone else's token sees nothing.
	code, _, _ = j.call(http.MethodGet, "/api/v1/payments/"+bookingA+"/page", "", map[string]string{"X-Visitor-Token": "sampah"})
	assert.Equal(t, 401, code)

	// FR-24: attempt → Midtrans charge with the locked amount, the booking-based order id, and Basic auth.
	attemptHdr := map[string]string{"X-Visitor-Token": j.visitor, "Idempotency-Key": "idem-key-journey-001"}
	code, _, raw = j.call(http.MethodPost, "/api/v1/payments/"+bookingA+"/attempts", `{"method":"VA_BCA"}`, attemptHdr)
	require.Equal(t, 201, code, string(raw))
	attempt := data[map[string]any](t, raw)
	assert.Equal(t, "PENDING", attempt["status"])
	assert.EqualValues(t, 4, attempt["bookingVersion"])
	assert.Equal(t, "812785002530231", attempt["instruction"].(map[string]any)["vaNumber"])
	req := j.midtrans.lastReq.Load().(map[string]any)
	assert.Equal(t, map[string]any{"order_id": orderA, "gross_amount": float64(1500000)}, req["transaction_details"])
	assert.Contains(t, j.midtrans.auth.Load().(string), "Basic ")
	assert.Equal(t, 1, int(j.booking.locks.Load()), "payment-lock dipanggil sebelum charge")

	// R3.6: the same key again returns the stored result and never charges twice.
	code, _, raw = j.call(http.MethodPost, "/api/v1/payments/"+bookingA+"/attempts", `{"method":"VA_BCA"}`, attemptHdr)
	require.Equal(t, 201, code)
	assert.Equal(t, "812785002530231", data[map[string]any](t, raw)["instruction"].(map[string]any)["vaNumber"])
	assert.EqualValues(t, 1, j.midtrans.charges.Load())
	// Same key, different payload → 409.
	code, _, _ = j.call(http.MethodPost, "/api/v1/payments/"+bookingA+"/attempts", `{"method":"QRIS"}`, attemptHdr)
	assert.Equal(t, 409, code)

	// Property 3: a forged webhook changes nothing and is answered 401.
	code, _, _ = j.call(http.MethodPost, "/api/v1/webhooks/midtrans", notification("settlement", signature(orderA, "200", "9.00")), nil)
	assert.Equal(t, 401, code)
	assert.Equal(t, 0, j.count(`SELECT count(*) FROM payment_attempts WHERE status = 'SUCCEEDED'`))
	assert.Equal(t, 0, j.count(`SELECT count(*) FROM outbox_events`))
	assert.Equal(t, 1, j.count(`SELECT count(*) FROM payment_callbacks WHERE signature_valid = false AND processing_result = 'REJECTED'`))

	// FR-25 / Property 4: the genuine settlement succeeds once, however often Midtrans repeats it.
	good := notification("settlement", signature(orderA, "200", "1500000.00"))
	for i := 0; i < 3; i++ {
		code, _, raw = j.call(http.MethodPost, "/api/v1/webhooks/midtrans", good, nil)
		require.Equal(t, 200, code, string(raw))
	}
	assert.Equal(t, 1, j.count(`SELECT count(*) FROM payment_attempts WHERE status = 'SUCCEEDED'`))
	assert.Equal(t, 1, j.count(`SELECT count(*) FROM outbox_events WHERE routing_key = 'payment.succeeded'`), "satu event untuk tiga webhook")
	assert.Equal(t, 1, j.count(`SELECT count(*) FROM payment_callbacks WHERE signature_valid AND processing_result = 'PROCESSED'`))
	// A late "pending" after settlement cannot move the status back.
	code, _, _ = j.call(http.MethodPost, "/api/v1/webhooks/midtrans", notification("pending", signature(orderA, "200", "1500000.00")), nil)
	assert.Equal(t, 200, code)
	assert.Equal(t, 1, j.count(`SELECT count(*) FROM payment_attempts WHERE status = 'SUCCEEDED'`))

	// FR-6 status is read-only and reflects the settlement.
	code, _, raw = j.call(http.MethodGet, "/api/v1/payments/"+bookingA+"/status", "", pageHdr)
	require.Equal(t, 200, code)
	assert.Equal(t, "SUCCEEDED", data[map[string]any](t, raw)["attempt"].(map[string]any)["status"])

	// NFR-10: the worker's outbox relay publishes payment.succeeded to RabbitMQ with the row id as messageId.
	var outboxID string
	require.NoError(t, j.pool.QueryRow(ctx, `SELECT id::text FROM outbox_events`).Scan(&outboxID))
	got := make(chan rabbitmq.Delivery, 4)
	cctx, cancel := context.WithCancel(ctx)
	defer cancel()
	go func() {
		_ = j.clients.MQ.Consume(cctx, rabbitmq.Subscription{Queue: "it.payment.journey", RoutingKeys: []string{"payment.succeeded"},
			Handler: func(_ context.Context, d rabbitmq.Delivery) error { got <- d; return nil }}, quiet)
	}()
	time.Sleep(700 * time.Millisecond) // topologi terpasang sebelum publish
	js, err := jobs(j.clients, quiet)
	require.NoError(t, err)
	require.Len(t, js, 1)
	require.NoError(t, js[0].Run(ctx))
	require.Eventually(t, func() bool {
		for {
			select {
			case d := <-got:
				if d.Envelope.MessageID == outboxID {
					assert.Equal(t, "payment-service", d.Envelope.Producer)
					assert.NoError(t, contract.ValidateFile(eventContracts+"envelope.schema.json", d.Envelope), "envelope sesuai kontrak")
					assert.NoError(t, contract.ValidateFile(eventContracts+"payment.succeeded.v1.schema.json", d.Envelope.Payload), "payload sesuai kontrak")
					var p map[string]any
					require.NoError(t, json.Unmarshal(d.Envelope.Payload, &p))
					assert.Equal(t, bookingA, p["bookingId"])
					assert.EqualValues(t, 1500000, p["amountMinor"])
					assert.Equal(t, "VA_BCA", p["method"])
					assert.Equal(t, "2026-10-26T10:30:05Z", p["settlementTime"], "settlement_time WIB → UTC")
					return true
				}
			default:
				return false
			}
		}
	}, 10*time.Second, 100*time.Millisecond, "event payment.succeeded tidak sampai ke RabbitMQ")
	assert.Equal(t, 0, j.count(`SELECT count(*) FROM outbox_events WHERE published_at IS NULL`))

	// Resolve: only Booking's service token is accepted; a settled attempt is reported with its settlement time.
	code, _, _ = j.call(http.MethodPost, "/internal/v1/payments/"+bookingA+"/resolve", `{"mode":"CHECK"}`, map[string]string{"Authorization": "Bearer " + j.visitor})
	assert.Equal(t, 401, code, "visitor token bukan service token")
	code, _, raw = j.call(http.MethodPost, "/internal/v1/payments/"+bookingA+"/resolve", `{"mode":"CHECK"}`, map[string]string{"Authorization": "Bearer " + j.service})
	require.Equal(t, 200, code, string(raw))
	res := data[map[string]any](t, raw)
	assert.Equal(t, "SUCCEEDED", res["outcome"])
	assert.Equal(t, "2026-10-26T10:30:05Z", res["settlementTime"])
	code, _, raw = j.call(http.MethodPost, "/internal/v1/payments/"+bookingB+"/resolve", `{"mode":"CHECK_AND_CANCEL"}`, map[string]string{"Authorization": "Bearer " + j.service})
	require.Equal(t, 200, code)
	assert.Equal(t, "NO_ATTEMPT", data[map[string]any](t, raw)["outcome"])

	// R2: a mobile WebView exchanges a one-time page token for an rb_pay cookie scoped to one booking.
	code, _, raw = j.call(http.MethodPost, "/api/v1/payments/"+bookingB+"/page-tokens", "", pageHdr)
	require.Equal(t, 201, code, string(raw))
	pt := data[map[string]any](t, raw)["pageToken"].(string)
	body := `{"pageToken":"` + pt + `"}`
	code, hdr, _ := j.call(http.MethodPost, "/api/v1/payment-page-sessions", body, nil)
	require.Equal(t, 204, code)
	cookie := (&http.Response{Header: hdr}).Cookies()
	require.Len(t, cookie, 1)
	assert.Equal(t, "rb_pay", cookie[0].Name)
	assert.True(t, cookie[0].HttpOnly)

	payCookie := map[string]string{"Cookie": "rb_pay=" + cookie[0].Value}
	code, _, _ = j.call(http.MethodGet, "/api/v1/payments/"+bookingB+"/status", "", payCookie)
	assert.Equal(t, 200, code, "cookie berlaku untuk booking-nya")
	code, _, _ = j.call(http.MethodGet, "/api/v1/payments/"+bookingA+"/status", "", payCookie)
	assert.Equal(t, 404, code, "cookie tidak berlaku untuk booking lain")
	code, _, _ = j.call(http.MethodPost, "/api/v1/payment-page-sessions", body, nil)
	assert.Equal(t, 403, code, "token WebView hanya sekali pakai")
	code, _, _ = j.call(http.MethodPost, "/api/v1/payments/"+bookingB+"/page-tokens", "", payCookie)
	assert.Equal(t, 401, code, "sesi WebView tidak bisa mencetak token baru")
}
