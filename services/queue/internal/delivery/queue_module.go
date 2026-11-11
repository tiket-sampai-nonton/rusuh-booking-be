package delivery

import (
	"context"
	"crypto/ed25519"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"sync"
	"time"

	"github.com/tiket-sampai-nonton/rusuh-booking-be/pkg/clock"
	"github.com/tiket-sampai-nonton/rusuh-booking-be/pkg/crypto"
	"github.com/tiket-sampai-nonton/rusuh-booking-be/pkg/rabbitmq"
	"github.com/tiket-sampai-nonton/rusuh-booking-be/pkg/resilience"
	"github.com/tiket-sampai-nonton/rusuh-booking-be/pkg/revocation"
	"github.com/tiket-sampai-nonton/rusuh-booking-be/pkg/token"

	"github.com/tiket-sampai-nonton/rusuh-booking-be/pkg/telemetry"
	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/queue/internal/config"
	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/queue/internal/queue/application/usecase"
	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/queue/internal/queue/infrastructure/bookingclient"
	queuehttp "github.com/tiket-sampai-nonton/rusuh-booking-be/services/queue/internal/queue/infrastructure/http"
	queuepg "github.com/tiket-sampai-nonton/rusuh-booking-be/services/queue/internal/queue/infrastructure/postgres"
	queuerabbitmq "github.com/tiket-sampai-nonton/rusuh-booking-be/services/queue/internal/queue/infrastructure/rabbitmq"
	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/queue/internal/queue/infrastructure/security"
	queuetelemetry "github.com/tiket-sampai-nonton/rusuh-booking-be/services/queue/internal/queue/infrastructure/telemetry"
	queuevalkey "github.com/tiket-sampai-nonton/rusuh-booking-be/services/queue/internal/queue/infrastructure/valkey"
	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/queue/internal/queue/infrastructure/worker"
	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/queue/internal/queue/port/outbound"
)

type registrar interface{ Register(*http.ServeMux) }

// queueModule bundles the wired HTTP handlers, worker jobs, and the Booking-event consumer.
type queueModule struct {
	handlers []registrar
	jobs     []Job
	consumer *queuerabbitmq.Consumer
	rmqConn  *rabbitmq.Connection
	depth    *queuetelemetry.DepthCollector // NFR-09 queue depth; registered by the API server only
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

	tokens := security.NewTokenIssuer(signer, cfg.Token.Issuer, cfg.Token.Audience, cfg.Token.AdmissionTTL, cfg.Token.VisitorTTL, cfg.Token.QueueTTL)
	// Device claim key: the PII HMAC key (validated at start); only the HMAC of X-Device-Id is ever kept.
	deviceKey, err := crypto.ParseKey(cfg.PII.HashKeyHex)
	if err != nil {
		return nil, err
	}
	deviceHasher, err := security.NewDeviceHasher(deviceKey)
	if err != nil {
		return nil, err
	}
	visitorAuth := security.NewVisitorAuthenticator(verifier, clk.Now).WithDeviceHasher(deviceHasher)

	userVerifierCore, err := loadUserVerifier(cfg, verifier)
	if err != nil {
		return nil, err
	}
	denylist := revocation.NewStore(clients.VK.VK(), clients.VK)
	userVerifier := security.NewUserTokenVerifier(userVerifierCore, clk.Now).WithDenylist(denylist) // logout mencabut access token (SCRUM-53)
	adminVerifier, err := newAdminVerifier(cfg, clk.Now, denylist)
	if err != nil {
		return nil, err
	}

	store := queuevalkey.NewQueueStore(clients.VK.VK(), clients.VK, clk, cfg.Liveness.Shards)
	limiter := queuevalkey.NewRateLimiter(clients.VK.VK(), clients.VK, clk.Now)
	settingsRepo := queuepg.NewEventSettings(clients.PG.Pool())
	settingsAdminRepo := queuepg.NewEventSettingsAdmin(clients.PG.Pool())
	auditRepo := queuepg.NewEntryAudit(clients.PG.Pool())

	breaker, err := resilience.NewBreaker(resilience.BreakerConfig{
		Name: "booking", FailureThreshold: 5, Cooldown: 10 * time.Second, HalfOpenProbes: 2, Timeout: cfg.Booking.Timeout,
		OnStateChange: telemetry.BreakerObserver(),
	}, clk.Now)
	if err != nil {
		return nil, err
	}
	booking := bookingclient.NewClient(cfg.Booking.BaseURL, cfg.Booking.Timeout, breaker, serviceToken(tokens, clk.Now))

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

	promoterMetrics := sharedPromoterMetrics()
	joinUC := usecase.NewJoinQueue(usecase.JoinDeps{
		Store: store, Settings: settingsRepo, Booking: booking, Limiter: limiter,
		Tokens: tokens, UserVerifier: userVerifier, Audit: auditRepo, Clock: clk,
	}, joinSettings(cfg)).WithMetrics(promoterMetrics).WithLogger(logger)
	statusUC := usecase.NewGetStatus(store)
	hbPolicy := heartbeatPolicy(cfg)
	myEntryUC := usecase.NewGetMyEntry(store).WithHeartbeat(hbPolicy).
		WithLimiter(limiter, cfg.RateLimit.MePerVisitor, cfg.RateLimit.MePerVisitorWindow)
	heartbeatUC := usecase.NewHeartbeat(store, clk, hbPolicy).
		WithLimiter(limiter, cfg.RateLimit.HeartbeatPerVisitor, cfg.RateLimit.MePerVisitorWindow)
	claimUC := usecase.NewClaimTurn(store, tokens, clk, cfg.Booking.PageBaseURL)
	leaveUC := usecase.NewLeaveQueue(store, booking, clk)
	getSettingsUC := usecase.NewGetQueueSettings(settingsRepo)
	setSettingsUC := usecase.NewSetQueueSettings(settingsRepo, booking, settingsAdminRepo, clk)

	trustProxy := cfg.App.Env != "local"
	handlers := []registrar{
		queuehttp.NewJoinHandler(joinUC, trustProxy).WithAuth(visitorAuth),
		queuehttp.NewStatusHandler(statusUC),
		queuehttp.NewMyEntryHandler(myEntryUC, visitorAuth),
		queuehttp.NewHeartbeatHandler(heartbeatUC, visitorAuth),
		queuehttp.NewClaimHandler(claimUC, visitorAuth),
		queuehttp.NewLeaveHandler(leaveUC, visitorAuth),
		queuehttp.NewSettingsAdminHandler(getSettingsUC, setSettingsUC, adminVerifier),
	}

	promoter := worker.NewPromoter(store, settingsRepo, booking, publisher, auditRepo, clk, logger, promoterTuning(cfg)).
		WithMetrics(promoterMetrics)
	liveness := worker.NewLiveness(store, settingsRepo, clk, livenessTuning(cfg))
	sweeper := worker.NewSweeper(store, settingsRepo, clk, sweepBatch(cfg))
	statusPub := worker.NewStatusPublisher(store, settingsRepo, booking, clk, statusTTL(cfg))
	jobs := buildJobs(cfg, promoter.Run, sweeper.Run, statusPub.Run, liveness.Run)

	consumer := queuerabbitmq.NewConsumer(store, clk)

	return &queueModule{handlers: handlers, jobs: jobs, consumer: consumer, rmqConn: rmqConn,
		depth: queuetelemetry.NewDepthCollector(settingsRepo, store)}, nil
}

// statusTTL derives the status-doc TTL from the status publisher's own cadence (QUEUE_STATUS_INTERVAL_MS).
func statusTTL(cfg config.Config) time.Duration {
	return cfg.WorkerTuning.StatusTTL(cfg.WorkerTuning.StatusInterval)
}

// jobEvery maps a job name to its configured interval. Liveness has no dedicated knob and uses WORKER_INTERVAL.
func jobEvery(cfg config.Config) map[string]time.Duration {
	return map[string]time.Duration{
		"promoter":         cfg.WorkerTuning.PromotorInterval,
		"sweeper":          cfg.WorkerTuning.SweeperInterval,
		"status-publisher": cfg.WorkerTuning.StatusInterval,
		"liveness":         cfg.Worker.Interval,
	}
}

// buildJobs assembles the worker jobs, each with its own interval from jobEvery.
func buildJobs(cfg config.Config, promoter, sweeper, statusPub, liveness func(context.Context) error) []Job {
	every := jobEvery(cfg)
	return []Job{
		{Name: "promoter", Every: every["promoter"], Run: promoter},
		{Name: "sweeper", Every: every["sweeper"], Run: sweeper},
		{Name: "status-publisher", Every: every["status-publisher"], Run: statusPub},
		{Name: "liveness", Every: every["liveness"], Run: liveness},
	}
}

var (
	promoterMetricsOnce sync.Once
	promoterMetricsInst *queuetelemetry.PromoterMetrics
)

// sharedPromoterMetrics registers the promoter counters on the process-wide registry exactly once (registering the
// same names twice panics), however many modules a process builds.
func sharedPromoterMetrics() *queuetelemetry.PromoterMetrics {
	promoterMetricsOnce.Do(func() { promoterMetricsInst = queuetelemetry.NewPromoterMetrics(telemetry.Default) })
	return promoterMetricsInst
}

// promoterTuning maps config to the promoter knobs (validated at start, D-24).
func promoterTuning(cfg config.Config) worker.PromoterTuning {
	t := cfg.WorkerTuning
	return worker.PromoterTuning{
		BatchMax: t.PromoteBatchMax, LeaseTTL: t.PromoterLeaseTTL,
		NoSupplySuspectTicks: t.NoSupplySuspectTicks,
	}
}

// heartbeatPolicy maps config to the server-controlled heartbeat interval (R03.1).
func heartbeatPolicy(cfg config.Config) usecase.HeartbeatPolicy {
	return usecase.HeartbeatPolicy{BaseMs: cfg.Liveness.HeartbeatInterval.Milliseconds(), NearMs: cfg.Liveness.NearInterval.Milliseconds()}
}

// livenessTuning maps config to the liveness sweeper (it shares the promoter lease TTL).
func livenessTuning(cfg config.Config) worker.LivenessTuning {
	l := cfg.Liveness
	return worker.LivenessTuning{StaleAfter: l.StaleAfter, EndAfter: l.EndAfter, BatchMax: l.BatchMax, LeaseTTL: cfg.WorkerTuning.PromoterLeaseTTL}
}

// sweepBatch is the max number of expired entries the sweeper ends per event and tick.
func sweepBatch(cfg config.Config) int { return cfg.WorkerTuning.SweepBatchMax }

// joinSettings maps config to the join usecase settings; the admission lease TTL matches the promoter's.
func joinSettings(cfg config.Config) usecase.JoinSettings {
	return usecase.JoinSettings{
		JoinPerIP: cfg.RateLimit.JoinPerIP, JoinPerIPWindow: cfg.RateLimit.JoinPerIPWindow,
		ClaimWindow: cfg.Token.AdmissionTTL, StatusURLTemplate: "/api/v1/queue-status/",
		AdmissionLeaseTTL: cfg.WorkerTuning.PromoterLeaseTTL,
		VisitorTokenTTL:   cfg.Token.VisitorTTL,
	}
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

// newAdminVerifier builds the verifier for the internal admin routes from the User Service public key and the
// ADMIN_EMAILS allow-list. Without TOKEN_PUBLIC_KEY_FILE it returns an untyped nil: the handler then denies every
// request. Unlike user tokens there is deliberately no fallback to Queue's own key (QSA-5.2): that key signs visitor,
// queue and service tokens, so trusting it for admin access would weaken the guard.
func newAdminVerifier(cfg config.Config, now func() time.Time, deny revocation.Denylist) (outbound.IAdminTokenVerifier, error) {
	if cfg.Token.PublicKeyFile == "" {
		return nil, nil
	}
	userKey, err := loadUserVerifier(cfg, nil)
	if err != nil {
		return nil, err
	}
	v := security.NewAdminVerifier(userKey, now, cfg.Admin.Emails)
	if deny != nil {
		v = v.WithDenylist(deny)
	}
	return v, nil
}

// serviceToken returns a function that gives Booking calls a valid service bearer token each time. The token is
// signed with Queue's private key and verified by Booking with Queue's public key (TOKEN_PUBLIC_KEY_PATH in
// Booking's config); it is renewed before it expires so a long-running worker keeps working.
func serviceToken(tokens *security.TokenIssuer, now func() time.Time) func() string {
	return func() string {
		tok, err := tokens.ServiceToken(now())
		if err != nil {
			return ""
		}
		return tok
	}
}
