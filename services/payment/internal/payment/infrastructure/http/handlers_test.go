package http_test

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/json"
	stdhttp "net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	"github.com/tiket-sampai-nonton/rusuh-booking-be/pkg/httpx"
	"github.com/tiket-sampai-nonton/rusuh-booking-be/pkg/token"

	mock_inbound "github.com/tiket-sampai-nonton/rusuh-booking-be/services/payment/_mock/payment/port/inbound"
	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/payment/internal/payment/domain/dto"
	paymenthttp "github.com/tiket-sampai-nonton/rusuh-booking-be/services/payment/internal/payment/infrastructure/http"
	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/payment/internal/payment/infrastructure/security"
	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/payment/internal/payment/port/inbound"
)

const (
	bookingID = "11111111-1111-1111-1111-111111111111"
	visitorID = "22222222-2222-2222-2222-222222222222"
	idemKey   = "idem-key-0123456789"
)

var t0 = time.Date(2026, 10, 26, 10, 0, 0, 0, time.UTC)

func now() time.Time { return t0.Add(time.Minute) }

// env is a payment handler wired with real token verification and mocked usecases.
type env struct {
	mux          *stdhttp.ServeMux
	page         *mock_inbound.MockIGetPaymentPage
	status       *mock_inbound.MockIGetPaymentStatus
	attempt      *mock_inbound.MockICreateAttempt
	createToken  *mock_inbound.MockICreatePageToken
	exchange     *mock_inbound.MockIExchangePageToken
	webhook      *mock_inbound.MockIHandleWebhook
	resolve      *mock_inbound.MockIResolvePayment
	visitorToken func() string
	pageCookie   func(visitor, booking string) string
	serviceToken func(issuer string) string
}

func newEnv(t *testing.T, secureCookie bool) env {
	t.Helper()
	c := gomock.NewController(t)
	e := env{
		mux:  stdhttp.NewServeMux(),
		page: mock_inbound.NewMockIGetPaymentPage(c), status: mock_inbound.NewMockIGetPaymentStatus(c),
		attempt: mock_inbound.NewMockICreateAttempt(c), createToken: mock_inbound.NewMockICreatePageToken(c),
		exchange: mock_inbound.NewMockIExchangePageToken(c), webhook: mock_inbound.NewMockIHandleWebhook(c),
		resolve: mock_inbound.NewMockIResolvePayment(c),
	}

	queuePub, queuePriv, _ := ed25519.GenerateKey(rand.Reader)
	queueSigner, _ := token.NewSigner(queuePriv, "queue-1")
	visitorVerifier, err := token.NewVerifier(map[string]ed25519.PublicKey{"queue-1": queuePub}, "rbt-queue", "rbt", 0)
	require.NoError(t, err)
	e.visitorToken = func() string {
		tok, err := queueSigner.Sign(token.Claims{Issuer: "rbt-queue", Subject: visitorID, Audience: "rbt", IssuedAt: t0.Unix(), ExpiresAt: t0.Add(time.Hour).Unix()})
		require.NoError(t, err)
		return tok
	}

	_, payPriv, _ := ed25519.GenerateKey(rand.Reader)
	session, err := security.NewPageSession(payPriv, "payment-1", "rbt-payment")
	require.NoError(t, err)
	e.pageCookie = func(visitor, booking string) string {
		v, _, err := session.Sign(visitor, booking, t0)
		require.NoError(t, err)
		return v
	}

	bookingPub, bookingPriv, _ := ed25519.GenerateKey(rand.Reader)
	bookingSigner, _ := token.NewSigner(bookingPriv, "booking-1")
	serviceVerifier, err := token.NewVerifier(map[string]ed25519.PublicKey{"booking-1": bookingPub}, "rbt-booking", "rbt-payment", 0)
	require.NoError(t, err)
	e.serviceToken = func(issuer string) string {
		tok, err := bookingSigner.Sign(token.Claims{Issuer: issuer, Subject: issuer, Audience: "rbt-payment", IssuedAt: t0.Unix(), ExpiresAt: t0.Add(5 * time.Minute).Unix()})
		require.NoError(t, err)
		return tok
	}

	auth := paymenthttp.NewPayerAuth(httpx.VisitorAuth(visitorVerifier, now), session, now)
	paymenthttp.NewPaymentHandler(e.page, e.status, e.attempt, e.createToken, e.exchange, auth, secureCookie).Register(e.mux)
	paymenthttp.NewWebhookHandler(e.webhook).Register(e.mux)
	paymenthttp.NewInternalHandler(e.resolve, httpx.ServiceAuth(now, serviceVerifier)).Register(e.mux)
	return e
}

func (e env) do(req *stdhttp.Request) *httptest.ResponseRecorder {
	rec := httptest.NewRecorder()
	e.mux.ServeHTTP(rec, req)
	return rec
}

func jsonReq(method, path, body string) *stdhttp.Request {
	r := httptest.NewRequest(method, path, strings.NewReader(body))
	r.Header.Set("Content-Type", "application/json")
	return r
}

func errorMessage(t *testing.T, rec *httptest.ResponseRecorder) string {
	t.Helper()
	var b struct{ Meta struct{ Message string } }
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &b))
	return b.Meta.Message
}

func TestPayerAuth_VisitorTokenFromCookieOrHeaderGivesSamePayer(t *testing.T) {
	e := newEnv(t, true)
	want := inbound.Payer{VisitorID: visitorID}
	e.page.EXPECT().GetPage(gomock.Any(), want, bookingID).Return(dto.PaymentPage{BookingID: bookingID}, nil).Times(2)

	web := httptest.NewRequest(stdhttp.MethodGet, "/api/v1/payments/"+bookingID+"/page", nil)
	web.AddCookie(&stdhttp.Cookie{Name: httpx.CookieVisitor, Value: e.visitorToken()})
	assert.Equal(t, 200, e.do(web).Code)

	mobile := httptest.NewRequest(stdhttp.MethodGet, "/api/v1/payments/"+bookingID+"/page", nil)
	mobile.Header.Set(httpx.HeaderVisitorToken, e.visitorToken())
	assert.Equal(t, 200, e.do(mobile).Code)
}

func TestPayerAuth_PageSessionCookieIsScopedToItsBooking(t *testing.T) {
	e := newEnv(t, true)
	e.status.EXPECT().GetStatus(gomock.Any(), inbound.Payer{VisitorID: visitorID, BookingID: bookingID}, bookingID).
		Return(dto.PaymentStatus{BookingID: bookingID}, nil)

	r := httptest.NewRequest(stdhttp.MethodGet, "/api/v1/payments/"+bookingID+"/status", nil)
	r.AddCookie(&stdhttp.Cookie{Name: paymenthttp.CookiePay, Value: e.pageCookie(visitorID, bookingID)})
	assert.Equal(t, 200, e.do(r).Code)
}

func TestPayerAuth_VisitorTokenWinsOverPageCookie(t *testing.T) {
	e := newEnv(t, true)
	e.page.EXPECT().GetPage(gomock.Any(), inbound.Payer{VisitorID: visitorID}, bookingID).Return(dto.PaymentPage{}, nil)
	r := httptest.NewRequest(stdhttp.MethodGet, "/api/v1/payments/"+bookingID+"/page", nil)
	r.Header.Set(httpx.HeaderVisitorToken, e.visitorToken())
	r.AddCookie(&stdhttp.Cookie{Name: paymenthttp.CookiePay, Value: e.pageCookie("lain", "lain")})
	assert.Equal(t, 200, e.do(r).Code)
}

func TestPayerAuth_Rejects(t *testing.T) {
	e := newEnv(t, true)
	for name, mod := range map[string]func(*stdhttp.Request){
		"tanpa kredensial":     func(*stdhttp.Request) {},
		"visitor token sampah": func(r *stdhttp.Request) { r.Header.Set(httpx.HeaderVisitorToken, "sampah") },
		"cookie rb_pay sampah": func(r *stdhttp.Request) { r.AddCookie(&stdhttp.Cookie{Name: paymenthttp.CookiePay, Value: "sampah"}) },
		"rb_pay kedaluwarsa": func(r *stdhttp.Request) {
			_, priv, _ := ed25519.GenerateKey(rand.Reader)
			s, _ := security.NewPageSession(priv, "payment-1", "rbt-payment")
			old, _, _ := s.Sign(visitorID, bookingID, t0.Add(-time.Hour))
			r.AddCookie(&stdhttp.Cookie{Name: paymenthttp.CookiePay, Value: old})
		},
	} {
		t.Run(name, func(t *testing.T) {
			r := httptest.NewRequest(stdhttp.MethodGet, "/api/v1/payments/"+bookingID+"/page", nil)
			mod(r)
			rec := e.do(r)
			assert.Equal(t, 401, rec.Code)
			assert.Contains(t, rec.Header().Get("WWW-Authenticate"), "Bearer")
		})
	}
}

func TestPageTokens_RequireVisitorTokenNotAPageSession(t *testing.T) {
	e := newEnv(t, true)
	r := httptest.NewRequest(stdhttp.MethodPost, "/api/v1/payments/"+bookingID+"/page-tokens", nil)
	r.AddCookie(&stdhttp.Cookie{Name: paymenthttp.CookiePay, Value: e.pageCookie(visitorID, bookingID)})
	assert.Equal(t, 401, e.do(r).Code, "sesi WebView tidak boleh mencetak token baru")

	e.createToken.EXPECT().CreatePageToken(gomock.Any(), inbound.Payer{VisitorID: visitorID}, bookingID).
		Return(dto.PageToken{PageToken: "tok", PageURL: "/pay/x", ExpiresAt: t0}, nil)
	r = httptest.NewRequest(stdhttp.MethodPost, "/api/v1/payments/"+bookingID+"/page-tokens", nil)
	r.Header.Set(httpx.HeaderVisitorToken, e.visitorToken())
	rec := e.do(r)
	assert.Equal(t, 201, rec.Code)
	assert.Equal(t, "no-store", rec.Header().Get("Cache-Control"))
}

func TestCreateAttempt_PassesIdempotencyKeyAndBodyAndReturns201Envelope(t *testing.T) {
	e := newEnv(t, true)
	e.attempt.EXPECT().CreateAttempt(gomock.Any(), inbound.Payer{VisitorID: visitorID}, bookingID, idemKey, dto.CreateAttemptRequest{Method: "VA_BCA"}).
		Return(dto.Attempt{AttemptNo: 1, Method: "VA_BCA", Status: "PENDING", RedirectURL: "https://snap/r/812"}, nil)

	r := jsonReq(stdhttp.MethodPost, "/api/v1/payments/"+bookingID+"/attempts", `{"method":"VA_BCA"}`)
	r.Header.Set("Idempotency-Key", idemKey)
	r.Header.Set(httpx.HeaderVisitorToken, e.visitorToken())
	rec := e.do(r)

	require.Equal(t, 201, rec.Code)
	var body struct {
		Meta struct{ Code int }
		Data dto.Attempt
	}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body))
	assert.Equal(t, 201, body.Meta.Code)
	assert.Equal(t, "https://snap/r/812", body.Data.RedirectURL)
}

func TestCreateAttempt_RejectsUnknownFieldsAndMapsUsecaseErrors(t *testing.T) {
	e := newEnv(t, true)
	r := jsonReq(stdhttp.MethodPost, "/api/v1/payments/"+bookingID+"/attempts", `{"method":"VA_BCA","amountMinor":1}`)
	r.Header.Set(httpx.HeaderVisitorToken, e.visitorToken())
	assert.Equal(t, 400, e.do(r).Code, "klien tidak boleh mengirim nominal")

	e.attempt.EXPECT().CreateAttempt(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).Return(dto.Attempt{}, inbound.ErrNotPayable)
	r = jsonReq(stdhttp.MethodPost, "/api/v1/payments/"+bookingID+"/attempts", `{"method":"VA_BCA"}`)
	r.Header.Set(httpx.HeaderVisitorToken, e.visitorToken())
	rec := e.do(r)
	assert.Equal(t, 409, rec.Code)
	assert.True(t, strings.HasPrefix(errorMessage(t, rec), "BOOKING_NOT_PAYABLE"))
}

func TestPageSessions_SetsHardenedCookie(t *testing.T) {
	exp := t0.Add(15 * time.Minute)
	for _, secure := range []bool{true, false} {
		e := newEnv(t, secure)
		e.exchange.EXPECT().ExchangePageToken(gomock.Any(), "token-satu-kali").Return(dto.PageSession{Cookie: "cookie-value", ExpiresAt: exp}, nil)
		rec := e.do(jsonReq(stdhttp.MethodPost, "/api/v1/payment-page-sessions", `{"pageToken":"token-satu-kali"}`))

		require.Equal(t, 204, rec.Code)
		cookies := rec.Result().Cookies()
		require.Len(t, cookies, 1)
		c := cookies[0]
		assert.Equal(t, "rb_pay", c.Name)
		assert.Equal(t, "cookie-value", c.Value)
		assert.True(t, c.HttpOnly)
		assert.Equal(t, secure, c.Secure)
		assert.Equal(t, stdhttp.SameSiteLaxMode, c.SameSite)
		assert.Equal(t, "no-store", rec.Header().Get("Cache-Control"))
	}
}

func TestPageSessions_InvalidTokenIs403WithoutCookie(t *testing.T) {
	e := newEnv(t, true)
	e.exchange.EXPECT().ExchangePageToken(gomock.Any(), "pernah-dipakai").Return(dto.PageSession{}, inbound.ErrPageTokenInvalid)
	rec := e.do(jsonReq(stdhttp.MethodPost, "/api/v1/payment-page-sessions", `{"pageToken":"pernah-dipakai"}`))
	assert.Equal(t, 403, rec.Code)
	assert.Empty(t, rec.Result().Cookies())
	assert.True(t, strings.HasPrefix(errorMessage(t, rec), "PAYMENT_PAGE_TOKEN_INVALID"))
}

func TestWebhook(t *testing.T) {
	const raw = `{"order_id":"RB-1-1","transaction_status":"settlement","status_code":"200","gross_amount":"1500000.00","signature_key":"s","extra_field":"diabaikan","va_numbers":[{"bank":"bca"}]}`

	t.Run("diterima, field tambahan diabaikan, body mentah diteruskan", func(t *testing.T) {
		e := newEnv(t, true)
		e.webhook.EXPECT().HandleWebhook(gomock.Any(), gomock.Any(), []byte(raw)).DoAndReturn(func(_ any, n dto.MidtransNotification, _ []byte) error {
			assert.Equal(t, "RB-1-1", n.OrderID)
			assert.Equal(t, "settlement", n.TransactionStatus)
			return nil
		})
		assert.Equal(t, 200, e.do(jsonReq(stdhttp.MethodPost, "/api/v1/webhooks/midtrans", raw)).Code)
	})
	t.Run("signature salah → 401", func(t *testing.T) {
		e := newEnv(t, true)
		e.webhook.EXPECT().HandleWebhook(gomock.Any(), gomock.Any(), gomock.Any()).Return(inbound.ErrSignatureInvalid)
		rec := e.do(jsonReq(stdhttp.MethodPost, "/api/v1/webhooks/midtrans", raw))
		assert.Equal(t, 401, rec.Code)
		assert.True(t, strings.HasPrefix(errorMessage(t, rec), "WEBHOOK_SIGNATURE_INVALID"))
	})
	t.Run("gangguan sementara → 503 agar Midtrans mengulang", func(t *testing.T) {
		e := newEnv(t, true)
		e.webhook.EXPECT().HandleWebhook(gomock.Any(), gomock.Any(), gomock.Any()).Return(inbound.ErrDependency)
		assert.Equal(t, 503, e.do(jsonReq(stdhttp.MethodPost, "/api/v1/webhooks/midtrans", raw)).Code)
	})
	t.Run("bukan JSON → 400", func(t *testing.T) {
		e := newEnv(t, true)
		assert.Equal(t, 400, e.do(jsonReq(stdhttp.MethodPost, "/api/v1/webhooks/midtrans", `<html>`)).Code)
	})
	t.Run("body terlalu besar → 413", func(t *testing.T) {
		e := newEnv(t, true)
		big := `{"order_id":"` + strings.Repeat("a", 70<<10) + `"}`
		assert.Equal(t, 413, e.do(jsonReq(stdhttp.MethodPost, "/api/v1/webhooks/midtrans", big)).Code)
	})
}

// Property 4: Internal API closed.
func TestInternalResolve_OnlyAKnownServiceTokenIsAccepted(t *testing.T) {
	e := newEnv(t, true)
	path := "/internal/v1/payments/" + bookingID + "/resolve"

	for name, mod := range map[string]func(*stdhttp.Request){
		"tanpa token":   func(*stdhttp.Request) {},
		"visitor token": func(r *stdhttp.Request) { r.Header.Set("Authorization", "Bearer "+e.visitorToken()) },
		"cookie sesi bayar": func(r *stdhttp.Request) {
			r.AddCookie(&stdhttp.Cookie{Name: paymenthttp.CookiePay, Value: e.pageCookie(visitorID, bookingID)})
		},
		"service token issuer lain": func(r *stdhttp.Request) { r.Header.Set("Authorization", "Bearer "+e.serviceToken("rbt-queue")) },
	} {
		t.Run(name, func(t *testing.T) {
			r := jsonReq(stdhttp.MethodPost, path, `{"mode":"CHECK"}`)
			mod(r)
			assert.Equal(t, 401, e.do(r).Code)
		})
	}

	e.resolve.EXPECT().ResolvePayment(gomock.Any(), bookingID, "CHECK_AND_CANCEL").
		Return(dto.ResolveResponse{Outcome: dto.OutcomeNotPaid, AttemptID: "a"}, nil)
	r := jsonReq(stdhttp.MethodPost, path, `{"mode":"CHECK_AND_CANCEL"}`)
	r.Header.Set("Authorization", "Bearer "+e.serviceToken("rbt-booking"))
	rec := e.do(r)
	require.Equal(t, 200, rec.Code)
	assert.Contains(t, rec.Body.String(), `"outcome":"NOT_PAID"`)

	r = jsonReq(stdhttp.MethodPost, path, `{"mode":"CHECK","amountMinor":1}`)
	r.Header.Set("Authorization", "Bearer "+e.serviceToken("rbt-booking"))
	assert.Equal(t, 400, e.do(r).Code)
}
