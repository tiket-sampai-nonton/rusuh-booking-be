package delivery

import (
	"crypto/ed25519"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"time"

	"github.com/tiket-sampai-nonton/rusuh-booking-be/pkg/clock"
	"github.com/tiket-sampai-nonton/rusuh-booking-be/pkg/crypto"
	"github.com/tiket-sampai-nonton/rusuh-booking-be/pkg/rabbitmq"
	"github.com/tiket-sampai-nonton/rusuh-booking-be/pkg/resilience"
	"github.com/tiket-sampai-nonton/rusuh-booking-be/pkg/token"

	"github.com/tiket-sampai-nonton/rusuh-booking-be/pkg/telemetry"
	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/queue/internal/config"
	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/queue/internal/queue/application/usecase"
	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/queue/internal/queue/infrastructure/bookingclient"
	queuehttp "github.com/tiket-sampai-nonton/rusuh-booking-be/services/queue/internal/queue/infrastructure/http"
	queuepg "github.com/tiket-sampai-nonton/rusuh-booking-be/services/queue/internal/queue/infrastructure/postgres"
	queuerabbitmq "github.com/tiket-sampai-nonton/rusuh-booking-be/services/queue/internal/queue/infrastructure/rabbitmq"
	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/queue/internal/queue/infrastructure/security"
	queuevalkey "github.com/tiket-sampai-nonton/rusuh-booking-be/services/queue/internal/queue/infrastructure/valkey"
	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/queue/internal/queue/infrastructure/worker"
)

type registrar interface{ Register(*http.ServeMux) }

// queueModule bundles the wired HTTP handlers, worker jobs, and the Booking-event consumer.
type queueModule struct {
	handlers []registrar
	jobs     []Job
	consumer *queuerabbitmq.Consumer
	rmqConn  *rabbitmq.Connection
}

// Register mounts every domain handler on the mux.
func (m *queueModule) Register(mux *http.ServeMux) {
	for _, h := range m.handlers {
		h.Register(mux)
	}
}

// newQueueModule wires adapters to usecases (composition root). Without Postgres/Valkey (e.g. the
// /health-only unit test) it returns an empty module so only /health is mounted.
func newQueueModule(cfg config.Config, clients *Clients, logger *slog.Logger) (*queueModule, error) {
	if clients.PG == nil || clients.VK == nil {
		return &queueModule{}, nil
	}
	clk := clock.System{}

	priv, signer, verifier, err := loadTokens(cfg)
	if err != nil {
		return nil, err
	}
	_ = priv

	encKey, _ := crypto.ParseKey(cfg.PII.EncKeyHex) // validated in config.Load
	hashKey, _ := crypto.ParseKey(cfg.PII.HashKeyHex)
	pii, err := security.NewPII(encKey, hashKey)
	if err != nil {
		return nil, err
	}

	tokens := security.NewTokenIssuer(signer, cfg.Token.Issuer, cfg.Token.Audience, cfg.Token.AdmissionTTL, cfg.Token.VisitorTTL, cfg.Token.QueueTTL)
	visitorAuth := security.NewVisitorAuthenticator(verifier, clk.Now)

	userVerifierCore, err := loadUserVerifier(cfg, verifier)
	if err != nil {
		return nil, err
	}
	userVerifier := security.NewUserTokenVerifier(userVerifierCore, clk.Now)

	store := queuevalkey.NewQueueStore(clients.VK.VK(), clients.VK, clk)
	limiter := queuevalkey.NewRateLimiter(clients.VK.VK(), clients.VK, clk.Now)
	settingsRepo := queuepg.NewEventSettings(clients.PG.Pool())
	auditRepo := queuepg.NewEntryAudit(clients.PG.Pool())

	breaker, err := resilience.NewBreaker(resilience.BreakerConfig{
		Name: "booking", FailureThreshold: 5, Cooldown: 10 * time.Second, HalfOpenProbes: 2, Timeout: cfg.Booking.Timeout,
		OnStateChange: telemetry.BreakerObserver(),
	}, clk.Now)
	if err != nil {
		return nil, err
	}
	booking := bookingclient.NewClient(cfg.Booking.BaseURL, cfg.Booking.Timeout, breaker, serviceToken(tokens))

	// Event publisher needs its own confirm-mode channel.
	rmqConn, err := rabbitmq.Dial(cfg.RabbitMQ)
	if err != nil {
		return nil, fmt.Errorf("queue: rabbitmq dial: %w", err)
	}
	rmqPub, err := rmqConn.NewPublisher()
	if err != nil {
		_ = rmqConn.Close()
		return nil, fmt.Errorf("queue: rabbitmq publisher: %w", err)
	}
	publisher := queuerabbitmq.NewEventPublisher(rmqPub, clk.Now)

	joinUC := usecase.NewJoinQueue(usecase.JoinDeps{
		Store: store, Settings: settingsRepo, Booking: booking, Limiter: limiter,
		Tokens: tokens, UserVerifier: userVerifier, PII: pii, Audit: auditRepo, Clock: clk,
	}, usecase.JoinSettings{
		JoinPerEmail: cfg.RateLimit.JoinPerEmail, JoinPerEmailWindow: cfg.RateLimit.JoinPerEmailWindow,
		JoinPerIP: cfg.RateLimit.JoinPerIP, JoinPerIPWindow: cfg.RateLimit.JoinPerIPWindow,
		ClaimWindow: cfg.Token.AdmissionTTL, StatusURLTemplate: "/api/v1/queue-status/",
	})
	statusUC := usecase.NewGetStatus(store)
	myEntryUC := usecase.NewGetMyEntry(store)
	claimUC := usecase.NewClaimTurn(store, tokens, clk, cfg.Booking.BaseURL+"/book")
	leaveUC := usecase.NewLeaveQueue(store, booking, clk)

	trustProxy := cfg.App.Env != "local"
	secure := cfg.App.Env != "local"
	handlers := []registrar{
		queuehttp.NewJoinHandler(joinUC, trustProxy, secure).WithAuth(visitorAuth),
		queuehttp.NewStatusHandler(statusUC),
		queuehttp.NewMyEntryHandler(myEntryUC, visitorAuth),
		queuehttp.NewClaimHandler(claimUC, visitorAuth),
		queuehttp.NewLeaveHandler(leaveUC, visitorAuth),
	}

	promoter := worker.NewPromoter(store, settingsRepo, booking, publisher, auditRepo, pii, clk, logger, cfg.WorkerTuning.PromoteBatchMax)
	sweeper := worker.NewSweeper(store, settingsRepo, clk)
	statusPub := worker.NewStatusPublisher(store, settingsRepo, booking, clk, cfg.WorkerTuning.StatusInterval)
	jobs := []Job{
		{Name: "promoter", Run: promoter.Run},
		{Name: "sweeper", Run: sweeper.Run},
		{Name: "status-publisher", Run: statusPub.Run},
	}

	consumer := queuerabbitmq.NewConsumer(store, clk)

	return &queueModule{handlers: handlers, jobs: jobs, consumer: consumer, rmqConn: rmqConn}, nil
}

// loadTokens reads the EdDSA private key and builds the signer and the verifier (own public key).
func loadTokens(cfg config.Config) (ed25519.PrivateKey, *token.Signer, *token.Verifier, error) {
	keyPEM, err := os.ReadFile(cfg.Token.PrivateKeyFile)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("queue: baca TOKEN_PRIVATE_KEY_FILE: %w", err)
	}
	priv, err := token.ParsePrivateKeyPEM(keyPEM)
	if err != nil {
		return nil, nil, nil, err
	}
	signer, err := token.NewSigner(priv, cfg.Token.KeyID)
	if err != nil {
		return nil, nil, nil, err
	}
	pub, ok := priv.Public().(ed25519.PublicKey)
	if !ok {
		return nil, nil, nil, errors.New("queue: public key tidak valid")
	}
	verifier, err := token.NewVerifier(map[string]ed25519.PublicKey{cfg.Token.KeyID: pub}, cfg.Token.Issuer, cfg.Token.Audience, 5*time.Second)
	if err != nil {
		return nil, nil, nil, err
	}
	return priv, signer, verifier, nil
}

// loadUserVerifier builds the verifier for User Service bearer tokens. It uses TOKEN_PUBLIC_KEY_FILE
// (the User Service public key) when set; otherwise it falls back to the service's own verifier
// (useful in local single-key setups).
func loadUserVerifier(cfg config.Config, fallback *token.Verifier) (*token.Verifier, error) {
	if cfg.Token.PublicKeyFile == "" {
		return fallback, nil
	}
	pubPEM, err := os.ReadFile(cfg.Token.PublicKeyFile)
	if err != nil {
		return nil, fmt.Errorf("queue: baca TOKEN_PUBLIC_KEY_FILE: %w", err)
	}
	pub, err := token.ParsePublicKeyPEM(pubPEM)
	if err != nil {
		return nil, err
	}
	v, err := token.NewVerifier(map[string]ed25519.PublicKey{cfg.Token.UserKeyID: pub}, cfg.Token.UserIssuer, cfg.Token.Audience, 5*time.Second)
	if err != nil {
		return nil, err
	}
	return v, nil
}

// serviceToken issues a long-lived EdDSA service bearer token used by the Queue Service when
// calling Booking's internal API. The token is signed with Queue's private key; Booking verifies
// it using Queue's public key (TOKEN_PUBLIC_KEY_PATH in Booking's config).
func serviceToken(tokens *security.TokenIssuer) string {
	tok, err := tokens.IssueService(time.Now())
	if err != nil {
		return ""
	}
	return tok
}
