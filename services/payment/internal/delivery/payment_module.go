package delivery

import (
	"crypto/ed25519"
	"fmt"
	"net/http"
	"os"
	"time"

	"github.com/tiket-sampai-nonton/rusuh-booking-be/pkg/clock"
	"github.com/tiket-sampai-nonton/rusuh-booking-be/pkg/httpx"
	"github.com/tiket-sampai-nonton/rusuh-booking-be/pkg/token"

	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/payment/internal/config"
	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/payment/internal/payment/application/usecase"
	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/payment/internal/payment/infrastructure/bookingclient"
	paymenthttp "github.com/tiket-sampai-nonton/rusuh-booking-be/services/payment/internal/payment/infrastructure/http"
	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/payment/internal/payment/infrastructure/midtrans"
	paymentpg "github.com/tiket-sampai-nonton/rusuh-booking-be/services/payment/internal/payment/infrastructure/postgres"
	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/payment/internal/payment/infrastructure/security"
)

// leeway is the clock skew tolerated when verifying tokens.
const leeway = 5 * time.Second

type registrar interface{ Register(*http.ServeMux) }

// newPaymentRegistrars builds the payment API: Midtrans gateway, repositories, usecases, and handlers. It fails
// fast when a secret or key file is missing (config.Payment.ValidateForServe).
func newPaymentRegistrars(cfg config.Config, clients *Clients) ([]registrar, error) {
	p := cfg.Payment
	if err := p.ValidateForServe(); err != nil {
		return nil, fmt.Errorf("payment: %w", err)
	}
	now := time.Now
	clk := clock.System{}

	visitorPub, err := readPublicKey(p.Token.VisitorPublicKeyFile)
	if err != nil {
		return nil, err
	}
	bookingPub, err := readPublicKey(p.Token.BookingPublicKeyFile)
	if err != nil {
		return nil, err
	}
	priv, err := readPrivateKey(p.Token.ServicePrivateKeyFile)
	if err != nil {
		return nil, err
	}
	visitorVerifier, err := token.NewVerifier(map[string]ed25519.PublicKey{p.Token.VisitorKeyID: visitorPub}, p.Token.VisitorIssuer, p.Token.VisitorAudience, leeway)
	if err != nil {
		return nil, err
	}
	// Calls from Booking carry a token issued by Booking for this service.
	bookingVerifier, err := token.NewVerifier(map[string]ed25519.PublicKey{p.Token.BookingKeyID: bookingPub}, p.Token.BookingIssuer, p.Token.ServiceIssuer, leeway)
	if err != nil {
		return nil, err
	}
	signer, err := token.NewSigner(priv, p.Token.ServiceKeyID)
	if err != nil {
		return nil, err
	}
	session, err := security.NewPageSession(priv, p.Token.ServiceKeyID, p.Token.ServiceIssuer)
	if err != nil {
		return nil, err
	}

	gateway, err := midtrans.New(midtrans.Config{
		BaseURL: p.Midtrans.BaseURL, ServerKey: p.Midtrans.ServerKey, NotificationURL: p.Midtrans.NotificationURL,
		Charge: limits(p.Midtrans.Charge), Status: limits(p.Midtrans.Status),
	}, now)
	if err != nil {
		return nil, err
	}
	bookingClient, err := bookingclient.New(p.BookingURL, httpx.NewServiceTokenSource(signer, p.Token.ServiceIssuer, p.Token.BookingAudience, now), nil, now)
	if err != nil {
		return nil, err
	}

	pool := clients.PG.Pool()
	attempts := paymentpg.NewAttempts(pool)
	callbacks := paymentpg.NewCallbacks(pool)
	idem := paymentpg.NewIdempotency(pool)
	pageTokens := paymentpg.NewPageTokens(pool)

	auth := paymenthttp.NewPayerAuth(httpx.VisitorAuth(visitorVerifier, now), session, now)
	secureCookie := cfg.App.Env != "local" // plain http://localhost cannot carry a Secure cookie

	return []registrar{
		paymenthttp.NewPaymentHandler(
			usecase.NewGetPaymentPage(bookingClient, attempts, p.Methods, clk),
			usecase.NewGetPaymentStatus(bookingClient, attempts, clk),
			usecase.NewCreateAttempt(bookingClient, attempts, idem, gateway, clk, p.Methods),
			usecase.NewCreatePageToken(bookingClient, pageTokens, clk, p.PageTokenTTL),
			usecase.NewExchangePageToken(pageTokens, session, clk),
			auth, secureCookie,
		),
		paymenthttp.NewWebhookHandler(usecase.NewHandleWebhook(midtrans.NewSignatureVerifier(p.Midtrans.ServerKey), attempts, callbacks, clk)),
		paymenthttp.NewInternalHandler(usecase.NewResolvePayment(attempts, gateway, clk), httpx.ServiceAuth(now, bookingVerifier)),
	}, nil
}

func limits(b config.Breaker) midtrans.Limits {
	return midtrans.Limits{Timeout: b.Timeout, FailureThreshold: b.FailureThreshold, Cooldown: b.Cooldown, Bulkhead: b.Bulkhead}
}

func readPublicKey(path string) (ed25519.PublicKey, error) {
	data, err := os.ReadFile(path) // #nosec G304 -- path comes from service configuration, not from a request
	if err != nil {
		return nil, fmt.Errorf("payment: baca kunci publik: %w", err)
	}
	return token.ParsePublicKeyPEM(data)
}

func readPrivateKey(path string) (ed25519.PrivateKey, error) {
	data, err := os.ReadFile(path) // #nosec G304 -- path comes from service configuration, not from a request
	if err != nil {
		return nil, fmt.Errorf("payment: baca kunci privat: %w", err)
	}
	return token.ParsePrivateKeyPEM(data)
}
