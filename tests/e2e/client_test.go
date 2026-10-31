//go:build e2e

package e2e

import (
	"bufio"
	"bytes"
	"crypto/sha512"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// Alamat service lokal (go run / make up). Dapat diganti lewat E2E_*_URL.
var (
	queueURL   = env("E2E_QUEUE_URL", "http://localhost:8081")
	bookingURL = env("E2E_BOOKING_URL", "http://localhost:8082")
	paymentURL = env("E2E_PAYMENT_URL", "http://localhost:8083")
	userURL    = env("E2E_USER_URL", "http://localhost:8085")
	mailpitURL = env("E2E_MAILPIT_URL", "http://localhost:8025")
)

func env(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}

var httpc = &http.Client{Timeout: 20 * time.Second}

// resp is a decoded {meta,data} envelope.
type resp struct {
	Status int
	Meta   map[string]any
	Data   map[string]any
	Raw    string
}

func (r resp) str(path ...string) string {
	var cur any = r.Data
	for _, p := range path {
		m, ok := cur.(map[string]any)
		if !ok {
			return ""
		}
		cur = m[p]
	}
	s, _ := cur.(string)
	return s
}

func call(t *testing.T, method, url string, headers map[string]string, body any) resp {
	t.Helper()
	var rd io.Reader
	if body != nil {
		raw, err := json.Marshal(body)
		require.NoError(t, err)
		rd = bytes.NewReader(raw)
	}
	req, err := http.NewRequest(method, url, rd)
	require.NoError(t, err)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	res, err := httpc.Do(req)
	require.NoError(t, err, "%s %s", method, url)
	defer func() { _ = res.Body.Close() }()
	raw, _ := io.ReadAll(res.Body)
	record(method, url, headers, body, res, raw)
	out := resp{Status: res.StatusCode, Raw: string(raw)}
	var env struct {
		Meta map[string]any `json:"meta"`
		Data map[string]any `json:"data"`
	}
	_ = json.Unmarshal(raw, &env)
	out.Meta, out.Data = env.Meta, env.Data
	return out
}

func mustStatus(t *testing.T, want int, r resp, what string) {
	t.Helper()
	require.Equal(t, want, r.Status, "%s: %s", what, r.Raw)
}

// eventually polls until fn returns true.
func eventually(t *testing.T, d time.Duration, what string, fn func() bool) {
	t.Helper()
	deadline := time.Now().Add(d)
	for time.Now().Before(deadline) {
		if fn() {
			return
		}
		time.Sleep(500 * time.Millisecond)
	}
	t.Fatalf("timeout menunggu: %s", what)
}

// ---- Mailpit ----

type mail struct{ ID, To, Subject, Text string }

func mailsTo(t *testing.T, addr string) []mail {
	t.Helper()
	r, err := httpc.Get(mailpitURL + "/api/v1/search?query=" + "to:" + addr)
	require.NoError(t, err)
	defer func() { _ = r.Body.Close() }()
	var list struct {
		Messages []struct {
			ID      string
			Subject string
			To      []struct{ Address string }
		}
	}
	require.NoError(t, json.NewDecoder(r.Body).Decode(&list))
	var out []mail
	for _, m := range list.Messages {
		d, err := httpc.Get(mailpitURL + "/api/v1/message/" + m.ID)
		require.NoError(t, err)
		var det struct{ Text, HTML string }
		_ = json.NewDecoder(d.Body).Decode(&det)
		_ = d.Body.Close()
		out = append(out, mail{ID: m.ID, To: addr, Subject: m.Subject, Text: det.Text + det.HTML})
	}
	return out
}

// ---- Midtrans server key (dipakai hanya untuk menandatangani webhook uji; tidak pernah dicetak) ----

func serverKey(t *testing.T) string {
	t.Helper()
	if k := os.Getenv("MIDTRANS_SERVER_KEY"); k != "" {
		return k
	}
	path := env("E2E_PAYMENT_ENV", filepath.Join("..", "..", "services", "payment", ".env"))
	f, err := os.Open(path)
	if err != nil {
		t.Skip("MIDTRANS_SERVER_KEY tidak diset dan .env payment tidak terbaca")
	}
	defer func() { _ = f.Close() }()
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		if k, ok := strings.CutPrefix(strings.TrimSpace(sc.Text()), "MIDTRANS_SERVER_KEY="); ok && k != "" {
			return strings.Trim(k, `"'`)
		}
	}
	t.Skip("MIDTRANS_SERVER_KEY kosong")
	return ""
}

// signature = SHA512(order_id + status_code + gross_amount + serverKey)
func signature(orderID, statusCode, gross, key string) string {
	sum := sha512.Sum512([]byte(orderID + statusCode + gross + key))
	return hex.EncodeToString(sum[:])
}

func notification(orderID, status, gross, key string) map[string]any {
	return map[string]any{
		"order_id": orderID, "transaction_status": status, "status_code": "200", "gross_amount": gross,
		"payment_type": "bank_transfer", "transaction_id": "e2e-" + orderID, "fraud_status": "accept",
		"transaction_time": time.Now().Format("2006-01-02 15:04:05"), "settlement_time": time.Now().Format("2006-01-02 15:04:05"),
		"signature_key": signature(orderID, "200", gross, key),
	}
}

func nikFor(serial int) string { return fmt.Sprintf("317401150190%04d", serial) }
