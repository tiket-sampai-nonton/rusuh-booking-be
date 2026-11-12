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
	"github.com/tiket-sampai-nonton/rusuh-booking-be/pkg/revocation"
	"github.com/tiket-sampai-nonton/rusuh-booking-be/pkg/token"

	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/user/internal/config"
	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/user/internal/user/application/usecase"
	userhttp "github.com/tiket-sampai-nonton/rusuh-booking-be/services/user/internal/user/infrastructure/http"
	userpg "github.com/tiket-sampai-nonton/rusuh-booking-be/services/user/internal/user/infrastructure/postgres"
	userrabbitmq "github.com/tiket-sampai-nonton/rusuh-booking-be/services/user/internal/user/infrastructure/rabbitmq"
	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/user/internal/user/infrastructure/security"
	uservalkey "github.com/tiket-sampai-nonton/rusuh-booking-be/services/user/internal/user/infrastructure/valkey"
)

type registrar interface{ Register(*http.ServeMux) }

type noRoutes struct{}

func (noRoutes) Register(*http.ServeMux) {}

// newUserModule wires the auth and profile usecases. Tanpa Postgres/Valkey (mis. di unit test /health)
// hanya /health yang terpasang.
func newUserModule(cfg config.Config, clients *Clients, logger *slog.Logger) (registrar, error) {
	if clients.PG == nil || clients.VK == nil {
		return noRoutes{}, nil
	}
	keyPEM, err := os.ReadFile(cfg.Token.PrivateKeyFile)
	if err != nil {
		return nil, fmt.Errorf("user: baca TOKEN_PRIVATE_KEY_FILE: %w", err)
	}
	priv, err := token.ParsePrivateKeyPEM(keyPEM)
	if err != nil {
		return nil, err
	}
	signer, err := token.NewSigner(priv, cfg.Token.KeyID)
	if err != nil {
		return nil, err
	}
	pub, ok := priv.Public().(ed25519.PublicKey)
	if !ok {
		return nil, errors.New("user: public key tidak valid")
	}
	verifier, err := token.NewVerifier(map[string]ed25519.PublicKey{cfg.Token.KeyID: pub}, cfg.Token.Issuer, cfg.Token.Audience, 5*time.Second)
	if err != nil {
		return nil, err
	}

	pii, err := newPII(cfg.PII)
	if err != nil {
		return nil, err
	}
	clk := clock.System{}
	rmqConn, err := rabbitmq.Dial(rabbitmq.Config{
		URL:          cfg.RabbitMQ.URL,
		Exchange:     cfg.RabbitMQ.Exchange,
		ExchangeType: "topic",
		Workers:      1,
		Prefetch:     1,
		MaxRetries:   0,
		ConfirmWait:  cfg.RabbitMQ.ConfirmWait,
	})
	if err != nil {
		return nil, fmt.Errorf("user: rabbitmq dial: %w", err)
	}
	rmqPub, err := rmqConn.NewPublisher()
	if err != nil {
		_ = rmqConn.Close()
		return nil, fmt.Errorf("user: rabbitmq publisher: %w", err)
	}
	clients.AddCloser("rabbitmq", rmqConn.Close)
	publisher := userrabbitmq.NewOTPPublisher(rmqPub)
	denylist := revocation.NewStore(clients.VK.VK(), clients.VK) // logout mencabut access token sesi itu (SCRUM-53)
	tokens := security.NewTokens(signer, verifier, cfg.Token.Issuer, cfg.Token.Audience, cfg.Token.AccessTTL, clk.Now).WithDenylist(denylist)
	accounts := userpg.NewAccounts(clients.PG.Pool())
	limiter := uservalkey.NewRateLimiter(clients.VK.VK(), clients.VK, clk.Now)

	auth := usecase.NewAuth(usecase.AuthDeps{
		Accounts:  accounts,
		Sessions:  userpg.NewSessions(clients.PG.Pool()),
		Audit:     userpg.NewAudit(clients.PG.Pool()),
		OTP:       uservalkey.NewOTPStore(clients.VK.VK(), clients.VK),
		Limiter:   limiter,
		Publisher: publisher,
		Tokens:    tokens,
		Secrets:   security.Secrets{},
		PII:       pii,
		Clock:     clk,
		Logger:    logger,
		Denylist:  denylist,
	}, usecase.AuthSettings{
		OTPTTL: cfg.OTP.TTL, ResendCooldown: cfg.OTP.ResendCooldown, MaxAttempts: cfg.OTP.MaxAttempts,
		MaxPerEmailHour: cfg.OTP.MaxPerEmailHour, MaxPerIPHour: cfg.OTP.MaxPerIPHour, RefreshTTL: cfg.Token.RefreshTTL,
		AccessTTL: cfg.Token.AccessTTL, MaxVerifyPerIPMinute: cfg.OTP.MaxVerifyPerIPMinute,
		MaxSessionPerIPMinute: cfg.Token.MaxRefreshPerIPMinute,
	})
	profile := usecase.NewProfile(accounts, pii, clk, limiter, denylist, cfg.Token.AccessTTL)
	// Di luar lokal service berada di belakang proxy tepercaya: IP klien diambil dari X-Forwarded-For sebanyak
	// APP_TRUSTED_PROXY_HOPS entri dari kanan. Hanya angkanya yang di-log.
	logger.Info("trusted proxy hops", "hops", cfg.App.TrustedProxyHops)
	return userhttp.NewAuthHandler(auth, profile, tokens, cfg.App.Env != "local", cfg.App.TrustedProxyHops), nil
}

// newPII parses the three keys and builds the protector. Kesalahan dikembalikan (bukan diabaikan) dan hanya
// menyebut nama env, tidak pernah isi kunci.
func newPII(c config.PII) (*security.PII, error) {
	encKey, err := crypto.ParseKey(c.EncKeyHex)
	if err != nil {
		return nil, fmt.Errorf("user: PII_ENC_KEY tidak valid: %w", err)
	}
	hashKey, err := crypto.ParseKey(c.HashKeyHex)
	if err != nil {
		return nil, fmt.Errorf("user: PII_HASH_KEY tidak valid: %w", err)
	}
	otpKey, err := crypto.ParseKey(c.OTPKeyHex)
	if err != nil {
		return nil, fmt.Errorf("user: OTP_HASH_KEY tidak valid: %w", err)
	}
	return security.NewPII(encKey, hashKey, otpKey)
}
