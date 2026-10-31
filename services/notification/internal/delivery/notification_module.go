package delivery

import (
	"context"
	"crypto/ed25519"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"time"

	"github.com/tiket-sampai-nonton/rusuh-booking-be/pkg/clock"
	"github.com/tiket-sampai-nonton/rusuh-booking-be/pkg/crypto"
	"github.com/tiket-sampai-nonton/rusuh-booking-be/pkg/token"

	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/notification/internal/config"
	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/notification/internal/notification/application/usecase"
	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/notification/internal/notification/infrastructure/emailguard"
	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/notification/internal/notification/infrastructure/fcm"
	notificationhttp "github.com/tiket-sampai-nonton/rusuh-booking-be/services/notification/internal/notification/infrastructure/http"
	notificationpg "github.com/tiket-sampai-nonton/rusuh-booking-be/services/notification/internal/notification/infrastructure/postgres"
	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/notification/internal/notification/infrastructure/qrcode"
	notificationmq "github.com/tiket-sampai-nonton/rusuh-booking-be/services/notification/internal/notification/infrastructure/rabbitmq"
	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/notification/internal/notification/infrastructure/resend"
	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/notification/internal/notification/infrastructure/security"
	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/notification/internal/notification/infrastructure/smtp"
	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/notification/internal/notification/infrastructure/template"
	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/notification/internal/notification/port/outbound"
)

type registrar interface{ Register(*http.ServeMux) }

type noRoutes struct{}

func (noRoutes) Register(*http.ServeMux) {}

// notificationModule holds the wired adapters, usecases, and consumers of the domain.
type notificationModule struct {
	subscriptionHandler registrar
	bookingConsumer     *notificationmq.BookingConsumer
	turnConsumer        *notificationmq.TurnConsumer
	otpConsumer         *notificationmq.OTPConsumer
}

// newNotificationModule wires every notification adapter and usecase. Without Postgres the module
// serves no routes and registers no consumers (e.g. /health-only unit tests).
func newNotificationModule(cfg config.Config, clients *Clients, logger *slog.Logger) (*notificationModule, error) {
	if clients.PG == nil {
		return &notificationModule{subscriptionHandler: noRoutes{}}, nil
	}

	encKey, err := crypto.ParseKey(cfg.PII.EncKeyHex)
	if err != nil {
		return nil, fmt.Errorf("notification: PII_ENC_KEY: %w", err)
	}
	hashKey, err := crypto.ParseKey(cfg.PII.HashKeyHex)
	if err != nil {
		return nil, fmt.Errorf("notification: PII_HASH_KEY: %w", err)
	}
	pii, err := security.NewPII(encKey, hashKey)
	if err != nil {
		return nil, err
	}

	tmpl, err := template.New()
	if err != nil {
		return nil, err
	}

	provider, err := newEmailSender(cfg, logger)
	if err != nil {
		return nil, err
	}
	email, err := emailguard.New(provider, clock.System{}.Now) // breaker + bulkhead for both providers (R9.5, R13.9)
	if err != nil {
		return nil, err
	}

	var push outbound.IPushSender
	if cfg.FCM.CredentialsPath != "" {
		push, err = fcm.NewFromCredentialsFile(cfg.FCM.ProjectID, cfg.FCM.CredentialsPath, nil)
		if err != nil {
			return nil, err
		}
	} else {
		push = noopPush{} // local/dev without FCM credentials: pushes are skipped
	}

	deliveries := notificationpg.NewDeliveries(clients.PG.Pool())
	subs := notificationpg.NewSubscriptions(clients.PG.Pool())
	clk := clock.System{}

	paid := usecase.NewNotifyBookingPaid(deliveries, tmpl, email, pii, qrcode.New(), clk, logger, cfg.Email.TicketLinkBaseURL)
	expired := usecase.NewNotifyBookingExpired(deliveries, tmpl, email, pii, clk, logger)
	review := usecase.NewNotifyBookingReview(deliveries, tmpl, email, pii, clk, logger)
	turnSoon := usecase.NewNotifyTurnSoon(deliveries, subs, push, pii, clk, logger)
	turnArrived := usecase.NewNotifyTurnArrived(deliveries, subs, push, pii, clk, logger)
	otp := usecase.NewNotifyOTPRequested(tmpl, email, pii, clk, logger)
	manageSub := usecase.NewManageSubscription(subs, pii, clk, cfg.Subscription.TTL)

	auth, err := visitorAuthenticator(cfg, clk)
	if err != nil {
		return nil, err
	}

	return &notificationModule{
		subscriptionHandler: notificationhttp.NewSubscriptionHandler(manageSub, auth),
		bookingConsumer:     notificationmq.NewBookingConsumer(paid, expired, review, logger),
		turnConsumer:        notificationmq.NewTurnConsumer(turnSoon, turnArrived, logger),
		otpConsumer:         notificationmq.NewOTPConsumer(otp, logger),
	}, nil
}

// newEmailSender picks the email adapter from EMAIL_PROVIDER: mailpit (SMTP; local and CI, nothing
// leaves the machine) or resend (real email, demo). The provider was already validated in config.
func newEmailSender(cfg config.Config, logger *slog.Logger) (outbound.IEmailSender, error) {
	switch cfg.Email.Provider {
	case config.EmailMailpit:
		return smtp.New(smtp.Config{
			Host: cfg.SMTP.Host, Port: cfg.SMTP.Port, User: cfg.SMTP.User,
			Password: cfg.SMTP.Password, From: cfg.Email.From, Timeout: cfg.SMTP.Timeout,
		}), nil
	case config.EmailResend:
		logger.Warn("EMAIL_PROVIDER=resend: email nyata akan dikirim lewat Resend (kuota akun gratis 100/hari)", "from", cfg.Email.From)
		return resend.New(resend.Config{APIKey: cfg.Email.ResendAPIKey, From: cfg.Email.From}), nil
	default:
		return nil, fmt.Errorf("notification: EMAIL_PROVIDER %q tidak dikenal", cfg.Email.Provider)
	}
}

// visitorAuthenticator builds the X-Visitor-Token verifier from the public key file. When no key is
// configured (local dev), every request is rejected as unauthorized by a nilAuth.
func visitorAuthenticator(cfg config.Config, clk clock.IClock) (notificationhttp.VisitorAuthenticator, error) {
	if cfg.Token.PublicKeyFile == "" {
		return denyAuth{}, nil
	}
	pem, err := os.ReadFile(cfg.Token.PublicKeyFile)
	if err != nil {
		return nil, fmt.Errorf("notification: baca TOKEN_PUBLIC_KEY_FILE: %w", err)
	}
	pub, err := token.ParsePublicKeyPEM(pem)
	if err != nil {
		return nil, err
	}
	verifier, err := token.NewVerifier(map[string]ed25519.PublicKey{cfg.Token.KeyID: pub}, cfg.Token.Issuer, cfg.Token.Audience, 5*time.Second)
	if err != nil {
		return nil, err
	}
	return security.NewVisitorAuthenticator(verifier, clk.Now), nil
}

// denyAuth rejects every request; used when no visitor public key is configured.
type denyAuth struct{}

func (denyAuth) Resolve(*http.Request) (string, bool) { return "", false }

// noopPush is a push sender that silently drops messages (local/dev without FCM credentials).
type noopPush struct{}

func (noopPush) Send(context.Context, outbound.PushMessage) (string, error) { return "noop", nil }
func (noopPush) IsTokenInvalid(error) bool                                  { return false }
