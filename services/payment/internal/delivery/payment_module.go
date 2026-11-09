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
	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/payment/internal/payment/infrastructure/simulator"
	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/payment/internal/payment/port/outbound"
)

// leeway is the clock skew tolerated when verifying tokens.
const leeway = 5 * time.Second

type registrar interface{ Register(*http.ServeMux) }

// newPaymentRegistrars builds the payment API: Midtrans gateway, repositories, usecases, and handlers. It fails
// fast when a secret or key file is missing (config.Payment.ValidateForServe).
func newPaymentRegistrars(cfg config.Config, clients *Clients) ([]registrar, error) {
	p := cfg.Payment
	if err := checkSimulatorEnv(cfg.App.Env, p.Midtrans.Environment); err != nil {
		return nil, err
	}
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

	midtransGateway, err := midtrans.New(midtrans.Config{
		BaseURL: p.Midtrans.BaseURL, SnapBaseURL: p.Midtrans.SnapBaseURL, ServerKey: p.Midtrans.ServerKey, NotificationURL: p.Midtrans.NotificationURL,
		Charge: limits(p.Midtrans.Charge), Status: limits(p.Midtrans.Status),
	}, now)
	if err != nil {
		return nil, err
	}
	// Local/ci only: the fault wrapper makes Status/Cancel ambiguous for the TIMEOUT scenario. Elsewhere the
	// real gateway is used unwrapped and nothing of the simulator is built.
	var gateway outbound.IPaymentGateway = midtransGateway
	var faults outbound.IGatewayFaults
	if simulatorEnabled(cfg.App.Env) {
		fg := simulator.NewFaultGateway(midtransGateway, clk)
		gateway, faults = fg, fg
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

	webhook := usecase.NewHandleWebhook(midtrans.NewSignatureVerifier(p.Midtrans.ServerKey), attempts, callbacks, clk)

	regs := []registrar{
		paymenthttp.NewPaymentHandler(
			usecase.NewGetPaymentPage(bookingClient, attempts, p.Methods, clk),
			usecase.NewGetPaymentStatus(bookingClient, attempts, clk),
			usecase.NewCreateAttempt(bookingClient, attempts, idem, gateway, clk, p.Methods, usecase.ReturnURLs{Success: p.SuccessReturnURL, Failed: p.FailedReturnURL}),
			usecase.NewCreatePageToken(bookingClient, pageTokens, clk, p.PageTokenTTL),
			usecase.NewExchangePageToken(pageTokens, session, clk),
			auth,
		),
		paymenthttp.NewWebhookHandler(webhook),
		paymenthttp.NewInternalHandler(usecase.NewResolvePayment(attempts, gateway, clk), httpx.ServiceAuth(now, bookingVerifier)),
	}
	if faults != nil {
		sim := usecase.NewSimulatePayment(attempts, bookingClient, webhook, midtrans.NewWebhookSigner(p.Midtrans.ServerKey), faults, clk)
		regs = append(regs, paymenthttp.NewSimulatorHandler(sim, auth))
	}
	return regs, nil
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
