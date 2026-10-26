package http

import (
	"context"
	stdhttp "net/http"
	"time"

	"github.com/tiket-sampai-nonton/rusuh-booking-be/pkg/apperror"
	"github.com/tiket-sampai-nonton/rusuh-booking-be/pkg/httpx"

	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/payment/internal/payment/port/inbound"
)

// CookiePay is the WebView session cookie (payment.yaml securityScheme paymentPageCookie).
const CookiePay = "rb_pay"

// PageSessionVerifier verifies the rb_pay cookie (infrastructure/security.PageSession).
type PageSessionVerifier interface {
	Verify(raw string, now time.Time) (visitorID, bookingID string, err error)
}

type payerKey struct{}

// PayerFrom returns the caller stored by PayerAuth.
func PayerFrom(ctx context.Context) (inbound.Payer, bool) {
	p, ok := ctx.Value(payerKey{}).(inbound.Payer)
	return p, ok
}

// PayerAuth authenticates the customer on payment endpoints. A visitor token (cookie rb_visitor or header
// X-Visitor-Token, web and mobile alike) identifies the visitor for any booking they own. Only when no visitor
// token is sent is the rb_pay cookie accepted, and then it is valid for that one booking.
type PayerAuth struct {
	visitor httpx.Middleware
	page    PageSessionVerifier
	now     func() time.Time
}

// NewPayerAuth builds the middleware factory. visitor is httpx.VisitorAuth for the Queue public key.
func NewPayerAuth(visitor httpx.Middleware, page PageSessionVerifier, now func() time.Time) *PayerAuth {
	return &PayerAuth{visitor: visitor, page: page, now: now}
}

// VisitorOnly accepts the visitor token only (page tokens must not be minted from a WebView session).
func (a *PayerAuth) VisitorOnly() httpx.Middleware { return a.middleware(false) }

// VisitorOrPageSession also accepts the rb_pay cookie.
func (a *PayerAuth) VisitorOrPageSession() httpx.Middleware { return a.middleware(true) }

func (a *PayerAuth) middleware(acceptPageSession bool) httpx.Middleware {
	return func(next stdhttp.Handler) stdhttp.Handler {
		viaVisitor := a.visitor(stdhttp.HandlerFunc(func(w stdhttp.ResponseWriter, r *stdhttp.Request) {
			v, _ := httpx.VisitorFrom(r.Context())
			next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), payerKey{}, inbound.Payer{VisitorID: v.ID})))
		}))
		return stdhttp.HandlerFunc(func(w stdhttp.ResponseWriter, r *stdhttp.Request) {
			if !acceptPageSession || hasVisitorToken(r) {
				viaVisitor.ServeHTTP(w, r)
				return
			}
			c, err := r.Cookie(CookiePay)
			if err != nil || c.Value == "" {
				viaVisitor.ServeHTTP(w, r) // answers 401
				return
			}
			visitorID, bookingID, err := a.page.Verify(c.Value, a.now())
			if err != nil {
				w.Header().Set("WWW-Authenticate", `Bearer realm="rbt-payment-page", error="invalid_token"`)
				httpx.WriteError(w, r, apperror.Wrap(err, apperror.CodeUnauthorized, stdhttp.StatusUnauthorized, "sesi halaman bayar tidak valid"))
				return
			}
			next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), payerKey{}, inbound.Payer{VisitorID: visitorID, BookingID: bookingID})))
		})
	}
}

func hasVisitorToken(r *stdhttp.Request) bool {
	if c, err := r.Cookie(httpx.CookieVisitor); err == nil && c.Value != "" {
		return true
	}
	return r.Header.Get(httpx.HeaderVisitorToken) != ""
}
