package delivery

import (
	"crypto/ed25519"
	"log/slog"
	"net/http"
	"os"
	"time"

	"github.com/tiket-sampai-nonton/rusuh-booking-be/pkg/clock"
	"github.com/tiket-sampai-nonton/rusuh-booking-be/pkg/crypto"
	"github.com/tiket-sampai-nonton/rusuh-booking-be/pkg/token"

	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/booking/internal/booking/application/usecase"
	bookinghttp "github.com/tiket-sampai-nonton/rusuh-booking-be/services/booking/internal/booking/infrastructure/http"
	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/booking/internal/booking/infrastructure/ids"
	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/booking/internal/booking/infrastructure/paymentclient"
	bookingpg "github.com/tiket-sampai-nonton/rusuh-booking-be/services/booking/internal/booking/infrastructure/postgres"
	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/booking/internal/booking/infrastructure/security"
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
	paymentClient := paymentclient.New(os.Getenv("PAYMENT_SERVICE_URL"), os.Getenv("BOOKING_SERVICE_TOKEN"))

	// Usecases.
	listEvents := usecase.NewListEvents(eventRepo, clk)
	getDetail := usecase.NewGetEventDetail(eventRepo)
	allocate := usecase.NewAllocateBatch(admissionRepo, idGen, clk)
	sessions := usecase.NewSession(admissionRepo, eventRepo, admissionVerifier, clk)
	attendee := usecase.NewCheckAttendee(admissionRepo, eventRepo, attendeeRepo, pii, clk)
	hold := usecase.NewHold(admissionRepo, eventRepo, idemStore, uow, pii, idGen, clk)
	edit := usecase.NewEdit(bookingRepo, admissionRepo, eventRepo, uow, pii, clk)
	tickets := usecase.NewGetTickets(bookingRepo, ticketRepo)
	history := usecase.NewListMyBookings(bookingRepo)
	expiry := usecase.NewExpire(bookingRepo, paymentClient, uow)

	// Handlers.
	regs := []registrar{
		bookinghttp.NewCatalogHandler(listEvents, getDetail),
		bookinghttp.NewSessionHandler(sessions, attendee),
		bookinghttp.NewBookingHandler(hold, edit, tickets),
		bookinghttp.NewMyBookingsHandler(history, bearerVerifier),
		bookinghttp.NewInternalHandler(allocate, bearerVerifier),
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

func loadVerifier(path, kid, issuer, audience string, now func() time.Time) *token.Verifier {
	if path == "" {
		return nil
	}
	data, err := os.ReadFile(path)
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
