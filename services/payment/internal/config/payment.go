package config

import (
	"errors"
	"fmt"
	"net/url"
	"strings"
	"time"

	"github.com/tiket-sampai-nonton/rusuh-booking-be/pkg/dotenv"
)

// Midtrans environments (MIDTRANS_ENVIRONMENT).
const (
	MidtransSandbox    = "sandbox"
	MidtransProduction = "production"
)

// Payment methods the service can offer (CreateAttemptRequest.method in payment.yaml).
var knownMethods = map[string]bool{"VA_BCA": true, "VA_BNI": true, "VA_BRI": true, "VA_PERMATA": true, "QRIS": true, "GOPAY": true}

// Breaker holds one circuit breaker + bulkhead setting (design §6).
type Breaker struct {
	Timeout          time.Duration
	FailureThreshold int
	Cooldown         time.Duration
	Bulkhead         int
}

// Midtrans holds the MIDTRANS_* settings. ServerKey is a secret and only read from the environment.
type Midtrans struct {
	Environment string
	BaseURL     string
	ServerKey   string
	// NotificationURL overrides the dashboard notification URL per charge (MIDTRANS_NOTIFICATION_URL). Local tests only.
	NotificationURL string
	Charge          Breaker // charge
	Status          Breaker // status check and cancel
}

// Token holds the key files for the three token flows Payment takes part in.
type Token struct {
	VisitorPublicKeyFile string // Queue public key: verifies visitor tokens (rb_visitor / X-Visitor-Token)
	VisitorKeyID         string
	VisitorIssuer        string
	VisitorAudience      string

	ServicePrivateKeyFile string // Payment private key: signs service tokens to Booking and the rb_pay cookie
	ServiceKeyID          string
	ServiceIssuer         string
	BookingAudience       string // audience of tokens sent to Booking

	BookingPublicKeyFile string // Booking public key: verifies Booking's calls to /internal/v1/payments/*
	BookingKeyID         string
	BookingIssuer        string
}

// Payment holds the payment-specific settings (task 0.1). Read by Load; the secrets are checked by
// ValidateForServe so migrate and healthcheck still run without them.
type Payment struct {
	Midtrans     Midtrans
	Methods      []string
	PageTokenTTL time.Duration
	BookingURL   string
	Token        Token
}

func loadPayment(r *dotenv.Reader) Payment {
	breaker := func(prefix string, timeout time.Duration, cooldown time.Duration, bulkhead int) Breaker {
		return Breaker{
			Timeout:          r.Duration(prefix+"_TIMEOUT", timeout),
			FailureThreshold: r.Int(prefix+"_FAILURE_THRESHOLD", 5),
			Cooldown:         r.Duration(prefix+"_COOLDOWN", cooldown),
			Bulkhead:         r.Int(prefix+"_BULKHEAD", bulkhead),
		}
	}
	env := r.String("MIDTRANS_ENVIRONMENT", MidtransSandbox)
	defaultURL := "https://api.sandbox.midtrans.com"
	if env == MidtransProduction {
		defaultURL = "https://api.midtrans.com"
	}
	var methods []string
	for _, m := range strings.Split(r.String("PAYMENT_METHODS_ENABLED", "VA_BCA,VA_BNI,VA_BRI,QRIS,GOPAY"), ",") {
		if m = strings.TrimSpace(m); m != "" {
			methods = append(methods, m)
		}
	}
	return Payment{
		Midtrans: Midtrans{
			Environment:     env,
			BaseURL:         r.String("MIDTRANS_BASE_URL", defaultURL),
			ServerKey:       r.String("MIDTRANS_SERVER_KEY", ""),
			NotificationURL: r.String("MIDTRANS_NOTIFICATION_URL", ""),
			Charge:          breaker("MIDTRANS_CHARGE", 3*time.Second, 20*time.Second, 20),
			Status:          breaker("MIDTRANS_STATUS", 2*time.Second, 15*time.Second, 10),
		},
		Methods:      methods,
		PageTokenTTL: time.Duration(r.Int("PAYMENT_PAGE_TOKEN_TTL_SECONDS", 60)) * time.Second,
		BookingURL:   r.String("BOOKING_SERVICE_URL", "http://localhost:8082"),
		Token: Token{
			VisitorPublicKeyFile: r.String("TOKEN_PUBLIC_KEY_FILE", ""),
			VisitorKeyID:         r.String("TOKEN_KEY_ID", "queue-1"),
			VisitorIssuer:        r.String("TOKEN_ISSUER", "rbt-queue"),
			VisitorAudience:      r.String("TOKEN_AUDIENCE", "rbt"),

			ServicePrivateKeyFile: r.String("SERVICE_TOKEN_PRIVATE_KEY_FILE", ""),
			ServiceKeyID:          r.String("SERVICE_TOKEN_KEY_ID", "payment-1"),
			ServiceIssuer:         r.String("SERVICE_TOKEN_ISSUER", "rbt-payment"),
			BookingAudience:       r.String("BOOKING_TOKEN_AUDIENCE", "rbt-booking"),

			BookingPublicKeyFile: r.String("BOOKING_TOKEN_PUBLIC_KEY_FILE", ""),
			BookingKeyID:         r.String("BOOKING_TOKEN_KEY_ID", "booking-1"),
			BookingIssuer:        r.String("BOOKING_TOKEN_ISSUER", "rbt-booking"),
		},
	}
}

// Validate checks value ranges. It does not require secrets.
func (p Payment) Validate() error {
	var errs []error
	switch p.Midtrans.Environment {
	case MidtransSandbox, MidtransProduction:
	default:
		errs = append(errs, fmt.Errorf("MIDTRANS_ENVIRONMENT %q tidak dikenal (%s | %s)", p.Midtrans.Environment, MidtransSandbox, MidtransProduction))
	}
	errs = append(errs, validURL("MIDTRANS_BASE_URL", p.Midtrans.BaseURL), validURL("BOOKING_SERVICE_URL", p.BookingURL))
	if u := p.Midtrans.NotificationURL; u != "" {
		if err := validURL("MIDTRANS_NOTIFICATION_URL", u); err != nil {
			errs = append(errs, err)
		} else if !strings.HasPrefix(u, "https://") {
			errs = append(errs, errors.New("MIDTRANS_NOTIFICATION_URL harus https"))
		}
	}
	errs = append(errs, validBreaker("MIDTRANS_CHARGE", p.Midtrans.Charge), validBreaker("MIDTRANS_STATUS", p.Midtrans.Status))
	if len(p.Methods) == 0 {
		errs = append(errs, errors.New("PAYMENT_METHODS_ENABLED kosong"))
	}
	for _, m := range p.Methods {
		if !knownMethods[m] {
			errs = append(errs, fmt.Errorf("PAYMENT_METHODS_ENABLED: metode %q tidak dikenal", m))
		}
	}
	if p.PageTokenTTL < 30*time.Second || p.PageTokenTTL > 120*time.Second {
		errs = append(errs, errors.New("PAYMENT_PAGE_TOKEN_TTL_SECONDS harus 30..120"))
	}
	return errors.Join(errs...)
}

// ValidateForServe checks what the api and worker need at start: the Midtrans server key and the token
// key files. Called when the payment module is built, so a missing secret stops the process (fail fast).
func (p Payment) ValidateForServe() error {
	var errs []error
	if p.Midtrans.ServerKey == "" {
		errs = append(errs, errors.New("MIDTRANS_SERVER_KEY wajib diisi"))
	}
	for name, v := range map[string]string{
		"TOKEN_PUBLIC_KEY_FILE":          p.Token.VisitorPublicKeyFile,
		"SERVICE_TOKEN_PRIVATE_KEY_FILE": p.Token.ServicePrivateKeyFile,
		"BOOKING_TOKEN_PUBLIC_KEY_FILE":  p.Token.BookingPublicKeyFile,
	} {
		if v == "" {
			errs = append(errs, fmt.Errorf("%s wajib diisi", name))
		}
	}
	return errors.Join(errs...)
}

func validURL(name, raw string) error {
	u, err := url.Parse(raw)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return fmt.Errorf("%s %q bukan URL http(s) yang valid", name, raw)
	}
	return nil
}

func validBreaker(name string, b Breaker) error {
	if b.Timeout <= 0 || b.FailureThreshold < 1 || b.Cooldown <= 0 || b.Bulkhead < 1 {
		return fmt.Errorf("%s_{TIMEOUT,FAILURE_THRESHOLD,COOLDOWN,BULKHEAD} harus > 0", name)
	}
	return nil
}
