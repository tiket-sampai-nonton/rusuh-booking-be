package http_test

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tiket-sampai-nonton/rusuh-booking-be/pkg/httpx"
	"github.com/tiket-sampai-nonton/rusuh-booking-be/pkg/token"

	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/booking/internal/booking/domain/dto"
	bookinghttp "github.com/tiket-sampai-nonton/rusuh-booking-be/services/booking/internal/booking/infrastructure/http"
)

type stubPayable struct{ calls int }

func (s *stubPayable) GetPayable(_ context.Context, id string) (dto.Payable, error) {
	s.calls++
	return dto.Payable{BookingID: id, PublicRef: "RB-7F3A9C21", Status: "PENDING_PAYMENT", AmountMinor: 150000}, nil
}

type stubLock struct{ calls int }

func (s *stubLock) Lock(context.Context, string, string) (dto.PaymentLockResponse, error) {
	s.calls++
	return dto.PaymentLockResponse{Outcome: "OK", Version: 2, AmountMinor: 150000}, nil
}

type issuer struct {
	signer *token.Signer
	pub    ed25519.PublicKey
}

func newIssuer(t *testing.T, kid string) issuer {
	t.Helper()
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	require.NoError(t, err)
	s, err := token.NewSigner(priv, kid)
	require.NoError(t, err)
	return issuer{signer: s, pub: pub}
}

func (i issuer) token(t *testing.T, c token.Claims) string {
	t.Helper()
	now := time.Now()
	c.IssuedAt, c.ExpiresAt = now.Unix(), now.Add(5*time.Minute).Unix()
	tok, err := i.signer.Sign(c)
	require.NoError(t, err)
	return tok
}

// userVerifier accepts any token signed by the issuer, like the service/user bearer verifiers that guard the old routes.
type userVerifier struct{ v *token.Verifier }

func (u userVerifier) VerifyAccess(tok string) (string, error) {
	c, err := u.v.Verify(tok, time.Now())
	return c.Subject, err
}

// F2 / payment Task 15.5: payable and payment-lock accept the Payment Service token and nothing else.
func TestInternalHandler_PaymentEndpointsAcceptOnlyThePaymentServiceToken(t *testing.T) {
	payment, queue, user := newIssuer(t, "payment-1"), newIssuer(t, "queue-1"), newIssuer(t, "user-1")

	paymentVerifier, err := token.NewVerifier(map[string]ed25519.PublicKey{"payment-1": payment.pub}, "rbt-payment", "rbt-booking", 5*time.Second)
	require.NoError(t, err)
	queueVerifier, err := token.NewVerifier(map[string]ed25519.PublicKey{"queue-1": queue.pub}, "rbt-queue", "rbt", 5*time.Second)
	require.NoError(t, err)

	payable, lock := &stubPayable{}, &stubLock{}
	mux := http.NewServeMux()
	// The legacy verifier accepts Queue tokens (service bearer) like the real module does; payment routes must ignore it.
	bookinghttp.NewInternalHandler(nil, userVerifier{queueVerifier}).
		WithPayable(payable).WithPaymentLock(lock).
		WithPaymentAuth(httpx.ServiceAuth(time.Now, paymentVerifier)).Register(mux)

	paymentTok := payment.token(t, token.Claims{Issuer: "rbt-payment", Subject: "rbt-payment", Audience: "rbt-booking"})
	cases := map[string]string{
		"tanpa token":                   "",
		"token service Queue":           queue.token(t, token.Claims{Issuer: "rbt-queue", Subject: "rbt-queue", Audience: "rbt"}),
		"admission token (kunci Queue)": queue.token(t, token.Claims{Issuer: "rbt-queue", Subject: "entry-1", Audience: "rbt", ID: "jti-1"}),
		"token user":                    user.token(t, token.Claims{Issuer: "rbt-user", Subject: "account-1", Audience: "rbt"}),
		"issuer Payment tetapi audience service lain": payment.token(t, token.Claims{Issuer: "rbt-payment", Subject: "rbt-payment", Audience: "rbt-notification"}),
		"token Payment berumur lebih dari 5 menit": func() string {
			now := time.Now()
			tok, err := payment.signer.Sign(token.Claims{Issuer: "rbt-payment", Subject: "rbt-payment", Audience: "rbt-booking", IssuedAt: now.Unix(), ExpiresAt: now.Add(time.Hour).Unix()})
			require.NoError(t, err)
			return tok
		}(),
	}
	routes := []struct{ method, path, body string }{
		{http.MethodGet, "/internal/v1/bookings/b1/payable", ""},
		{http.MethodPost, "/internal/v1/bookings/b1/payment-lock", `{"attemptKey":"k"}`},
	}
	for name, tok := range cases {
		for _, rt := range routes {
			t.Run(name+" "+rt.method, func(t *testing.T) {
				req := httptest.NewRequest(rt.method, rt.path, strings.NewReader(rt.body))
				req.Header.Set("Content-Type", "application/json")
				if tok != "" {
					req.Header.Set("Authorization", "Bearer "+tok)
				}
				rec := httptest.NewRecorder()
				mux.ServeHTTP(rec, req)
				assert.Equal(t, http.StatusUnauthorized, rec.Code)
			})
		}
	}
	assert.Zero(t, payable.calls+lock.calls, "use case tidak boleh dijalankan untuk panggilan yang ditolak")

	for _, rt := range routes {
		req := httptest.NewRequest(rt.method, rt.path, strings.NewReader(rt.body))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Authorization", "Bearer "+paymentTok)
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, req)
		assert.Equal(t, http.StatusOK, rec.Code, rt.path)
	}
	assert.Equal(t, 1, payable.calls)
	assert.Equal(t, 1, lock.calls)
}

// Without a configured Payment verifier the endpoints refuse everything: the guard fails closed (audit F2).
func TestInternalHandler_PaymentEndpointsFailClosedWithoutAuthConfig(t *testing.T) {
	queue := newIssuer(t, "queue-1")
	queueVerifier, err := token.NewVerifier(map[string]ed25519.PublicKey{"queue-1": queue.pub}, "rbt-queue", "rbt", 5*time.Second)
	require.NoError(t, err)
	tok := queue.token(t, token.Claims{Issuer: "rbt-queue", Subject: "rbt-queue", Audience: "rbt"})

	for name, h := range map[string]*bookinghttp.InternalHandler{
		"WithPaymentAuth tidak dipasang, verifier lama ada": bookinghttp.NewInternalHandler(nil, userVerifier{queueVerifier}),
		"tidak ada verifier sama sekali":                    bookinghttp.NewInternalHandler(nil, nil),
		"ServiceAuth tanpa verifier":                        bookinghttp.NewInternalHandler(nil, nil).WithPaymentAuth(httpx.ServiceAuth(time.Now)),
	} {
		t.Run(name, func(t *testing.T) {
			payable, lock := &stubPayable{}, &stubLock{}
			mux := http.NewServeMux()
			h.WithPayable(payable).WithPaymentLock(lock).Register(mux)
			for _, tk := range []string{"", tok} {
				req := httptest.NewRequest(http.MethodGet, "/internal/v1/bookings/b1/payable", nil)
				if tk != "" {
					req.Header.Set("Authorization", "Bearer "+tk)
				}
				rec := httptest.NewRecorder()
				mux.ServeHTTP(rec, req)
				assert.Equal(t, http.StatusUnauthorized, rec.Code)
			}
			assert.Zero(t, payable.calls)
		})
	}
}
