package delivery

import (
	"crypto/ed25519"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"time"

	"github.com/tiket-sampai-nonton/rusuh-booking-be/pkg/clock"
	"github.com/tiket-sampai-nonton/rusuh-booking-be/pkg/crypto"
	"github.com/tiket-sampai-nonton/rusuh-booking-be/pkg/httpx"
	"github.com/tiket-sampai-nonton/rusuh-booking-be/pkg/token"

	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/booking/internal/booking/application/usecase"
	bookinghttp "github.com/tiket-sampai-nonton/rusuh-booking-be/services/booking/internal/booking/infrastructure/http"
	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/booking/internal/booking/infrastructure/ids"
	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/booking/internal/booking/infrastructure/paymentclient"
	bookingpg "github.com/tiket-sampai-nonton/rusuh-booking-be/services/booking/internal/booking/infrastructure/postgres"
	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/booking/internal/booking/infrastructure/security"
	bookingvalkey "github.com/tiket-sampai-nonton/rusuh-booking-be/services/booking/internal/booking/infrastructure/valkey"
	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/booking/internal/config"
)

type registrar interface{ Register(*http.ServeMux) }

type noRoutes struct{}

func (noRoutes) Register(*http.ServeMux) {}

// module bundles the wired usecases so both the HTTP router and the worker can reuse them.
type module struct {
	registrars []registrar
	sweepSess  *usecase.SweepSessions
	sweepExp   *usecase.SweepExpiries
	settle     *usecase.Settle
	outbox     *bookingpg.OutboxWriter
}

// newBookingModule wires every Booking usecase, adapter, and handler. Without Postgres (e.g. /health
// only in a unit test) it mounts nothing extra, matching the user module pattern.
func newBookingModule(cfg config.Config, clients *Clients, logger *slog.Logger) (*module, error) {
	if clients.PG == nil {
		return &module{registrars: []registrar{noRoutes{}}}, nil
	}
	pool := clients.PG.Pool()
	clk := clock.System{}
	idGen := ids.New()

	// Outbound adapters.
	eventRepo := bookingpg.NewEventRepo(pool)
	eventWriteRepo := bookingpg.NewEventWriteRepo(pool)
	admissionRepo := bookingpg.NewAdmissionRepo(pool)
	bookingRepo := bookingpg.NewBookingRepo(pool)
	ticketRepo := bookingpg.NewTicketRepo(pool)
	attendeeRepo := bookingpg.NewAttendeeCheckRepo(pool)
	idemStore := bookingpg.NewIdempotencyStore(pool)
	outboxWriter := bookingpg.NewOutboxWriter(pool)
	uow, err := bookingpg.NewUnitOfWork(pool)
	if err != nil {
		return nil, err
	}

	pii, err := buildPII()
	if err != nil {
		return nil, err
	}
	admissionVerifier, bearerVerifier := buildTokenVerifiers(clk.Now)
	paymentClient := paymentclient.New(os.Getenv("PAYMENT_SERVICE_URL"), buildPaymentAuthorizer(logger, clk.Now))
	paymentAuth := buildPaymentAuth(logger, clk.Now)

	// Usecases.
	listEvents := usecase.NewListEvents(eventRepo, clk)
	getDetail := usecase.NewGetEventDetail(eventRepo)
	createEvent := usecase.NewCreateEvent(eventWriteRepo, idGen)
	updateConfig := usecase.NewUpdateEventConfig(eventWriteRepo)
	allocate := usecase.NewAllocateBatch(admissionRepo, idGen, clk)
	supply := usecase.NewGetEventSupply(eventRepo)
	sessions := usecase.NewSession(admissionRepo, eventRepo, admissionVerifier, clk)
	attendee := usecase.NewCheckAttendee(admissionRepo, eventRepo, attendeeRepo, pii, clk)
	cancelSess := usecase.NewCancelSession(admissionRepo)
	hold := usecase.NewHold(admissionRepo, eventRepo, idemStore, uow, pii, idGen, clk)
	edit := usecase.NewEdit(bookingRepo, admissionRepo, eventRepo, uow, pii, clk)
	tickets := usecase.NewGetTickets(bookingRepo, ticketRepo)
	history := usecase.NewListMyBookings(bookingRepo)
	getBooking := usecase.NewGetBooking(bookingRepo, clk)
	payable := usecase.NewGetPayable(bookingRepo, clk)
	deadlineUC := usecase.NewExtendDeadline(uow)
	lockUC := usecase.NewPaymentLock(uow, clk)
	releaseAlloc := usecase.NewReleaseAllocation(admissionRepo)
	expiry := usecase.NewExpire(
		bookingRepo,
		paymentClient,
		uow,
		cfg.Worker.VerifyGraceSeconds,
		func(string) string { return idGen.NewPublicRef() },
		nil, // ticketLinkTokenFn: nil → empty token (ticket-link signing not needed on expiry path)
	)
	cancelBook := usecase.NewCancelBooking(bookingRepo, paymentClient, uow, clk)

	// Handlers.
	regs := []registrar{
		bookinghttp.NewCatalogHandler(listEvents, getDetail),
		bookinghttp.NewSessionHandler(sessions, attendee, cancelSess),
		bookinghttp.NewBookingHandler(hold, edit, tickets, getBooking, cancelBook),
		bookinghttp.NewMyBookingsHandler(history, bearerVerifier),
		bookinghttp.NewInternalHandler(allocate, bearerVerifier).WithSupply(supply).WithPayable(payable).WithDeadlineExtension(deadlineUC).WithPaymentLock(lockUC).WithReleaseAllocation(releaseAlloc).WithPaymentAuth(paymentAuth),
		bookinghttp.NewAdminHandler(createEvent, updateConfig, bearerVerifier),
	}

	// SSE availability + timer stream (requires Valkey; skipped when it is not wired).
	if clients.VK != nil {
		cache := bookingvalkey.NewAvailabilityCache(clients.VK.VK())
		streamUC := usecase.NewStreamSession(admissionRepo, cache, clk, 3*time.Second, 1000)
		regs = append(regs, bookinghttp.NewSSEHandler(streamUC))
	}

	return &module{
		registrars: regs,
		sweepSess:  usecase.NewSweepSessions(admissionRepo, clk),
		sweepExp:   usecase.NewSweepExpiries(bookingRepo, expiry, clk),
		settle:     usecase.NewSettle(uow, idGen),
		outbox:     outboxWriter,
	}, nil
}

// Register mounts every handler of the module.
func (m *module) Register(mux *http.ServeMux) {
	for _, r := range m.registrars {
		r.Register(mux)
	}
}

func buildPII() (*security.PII, error) {
	hashKey, err := crypto.ParseKey(getenvOr("PII_HASH_KEY", devKeyHex))
	if err != nil {
		return nil, err
	}
	encKey, err := crypto.ParseKey(getenvOr("PII_ENC_KEY", devKeyHex))
	if err != nil {
		return nil, err
	}
	return security.NewPII(hashKey, encKey)
}

func buildTokenVerifiers(now func() time.Time) (*security.AdmissionVerifier, bookinghttp.IUserTokenVerifier) {
	// Queue public key: verifies admission tokens + service bearer (internal allocations).
	queueVerifier := loadVerifier(
		os.Getenv("TOKEN_PUBLIC_KEY_PATH"),
		getenvOr("TOKEN_KEY_ID", "queue-1"),
		getenvOr("TOKEN_ISSUER", "rbt-queue"),
		getenvOr("TOKEN_AUDIENCE", "rbt"),
		now,
	)
	// User public key: verifies buyer bearer tokens (/me/bookings).
	userVerifier := loadVerifier(
		os.Getenv("USER_TOKEN_PUBLIC_KEY_PATH"),
		getenvOr("USER_TOKEN_KEY_ID", "user-1"),
		getenvOr("USER_TOKEN_ISSUER", "rbt-user"),
		getenvOr("TOKEN_AUDIENCE", "rbt"),
		now,
	)
	return security.NewAdmissionVerifier(queueVerifier, now),
		security.NewMultiBearerVerifier(userVerifier, queueVerifier, now)
}

// buildPaymentAuthorizer makes the short-lived service token (5 minutes, cached) Booking sends to Payment's
// /internal/v1/payments/*. Without the private key every call fails, so expiry and cancel keep the stock held.
func buildPaymentAuthorizer(logger *slog.Logger, now func() time.Time) paymentclient.Authorizer {
	signer, err := loadSigner(os.Getenv("SERVICE_TOKEN_PRIVATE_KEY_PATH"), getenvOr("SERVICE_TOKEN_KEY_ID", "booking-1"))
	if err != nil {
		logger.Error("service token ke Payment tidak bisa dibuat; expiry dan cancel menolak sampai SERVICE_TOKEN_PRIVATE_KEY_PATH diisi", "error", err)
		return unavailableAuthorizer{err: err}
	}
	return httpx.NewServiceTokenSource(signer, getenvOr("SERVICE_TOKEN_ISSUER", "rbt-booking"), getenvOr("PAYMENT_TOKEN_AUDIENCE", "rbt-payment"), now)
}

// buildPaymentAuth guards payable and payment-lock with the Payment Service's token only (issuer rbt-payment, audience
// this service). With no key configured httpx.ServiceAuth has no verifier and refuses every request.
func buildPaymentAuth(logger *slog.Logger, now func() time.Time) httpx.Middleware {
	v := loadVerifier(os.Getenv("PAYMENT_TOKEN_PUBLIC_KEY_PATH"), getenvOr("PAYMENT_TOKEN_KEY_ID", "payment-1"),
		getenvOr("PAYMENT_TOKEN_ISSUER", "rbt-payment"), getenvOr("SERVICE_TOKEN_ISSUER", "rbt-booking"), now)
	if v == nil {
		logger.Error("PAYMENT_TOKEN_PUBLIC_KEY_PATH tidak diset atau tidak terbaca; /payable dan /payment-lock menolak semua permintaan")
		return httpx.ServiceAuth(now)
	}
	return httpx.ServiceAuth(now, v)
}

type unavailableAuthorizer struct{ err error }

func (a unavailableAuthorizer) Authorize(*http.Request) error { return a.err }

func loadSigner(path, kid string) (*token.Signer, error) {
	if path == "" {
		return nil, errors.New("SERVICE_TOKEN_PRIVATE_KEY_PATH kosong")
	}
	data, err := os.ReadFile(path) // #nosec G304 G703 -- path comes from service configuration, not from a request
	if err != nil {
		return nil, err
	}
	key, err := token.ParsePrivateKeyPEM(data)
	if err != nil {
		return nil, err
	}
	return token.NewSigner(key, kid)
}

func loadVerifier(path, kid, issuer, audience string, now func() time.Time) *token.Verifier {
	if path == "" {
		return nil
	}
	data, err := os.ReadFile(path) // #nosec G304 G703 -- path comes from service configuration, not from a request
	if err != nil {
		return nil
	}
	pub, err := token.ParsePublicKeyPEM(data)
	if err != nil {
		return nil
	}
	v, err := token.NewVerifier(map[string]ed25519.PublicKey{kid: pub}, issuer, audience, 5*time.Second)
	if err != nil {
		return nil
	}
	return v
}

func getenvOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

// devKeyHex is a 32-byte all-zero key used only when PII_* env is unset (local/dev). Production MUST
// set PII_HASH_KEY and PII_ENC_KEY (security-privacy.md); secrets never live in code.
const devKeyHex = "0000000000000000000000000000000000000000000000000000000000000000"
