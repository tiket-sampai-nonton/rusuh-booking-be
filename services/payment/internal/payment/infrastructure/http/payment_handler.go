// Package http contains the HTTP adapter (net/http) of the Payment domain.
package http

import (
	stdhttp "net/http"

	"github.com/tiket-sampai-nonton/rusuh-booking-be/pkg/httpx"

	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/payment/internal/payment/domain/dto"
	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/payment/internal/payment/port/inbound"
)

// PaymentHandler serves the customer-facing payment endpoints (payment.yaml).
type PaymentHandler struct {
	page         inbound.IGetPaymentPage
	status       inbound.IGetPaymentStatus
	attempt      inbound.ICreateAttempt
	createToken  inbound.ICreatePageToken
	exchange     inbound.IExchangePageToken
	auth         *PayerAuth
	secureCookie bool
}

// NewPaymentHandler wires the handler. secureCookie is false only for plain-http local development.
func NewPaymentHandler(page inbound.IGetPaymentPage, status inbound.IGetPaymentStatus, attempt inbound.ICreateAttempt,
	createToken inbound.ICreatePageToken, exchange inbound.IExchangePageToken, auth *PayerAuth, secureCookie bool) *PaymentHandler {
	return &PaymentHandler{page: page, status: status, attempt: attempt, createToken: createToken, exchange: exchange, auth: auth, secureCookie: secureCookie}
}

// Register mounts the routes.
func (h *PaymentHandler) Register(mux *stdhttp.ServeMux) {
	payerAuth := h.auth.VisitorOrPageSession()
	mux.Handle("GET /api/v1/payments/{bookingId}/page", httpx.Chain(stdhttp.HandlerFunc(h.getPage), payerAuth))
	mux.Handle("GET /api/v1/payments/{bookingId}/status", httpx.Chain(stdhttp.HandlerFunc(h.getStatus), payerAuth))
	mux.Handle("POST /api/v1/payments/{bookingId}/attempts", httpx.Chain(stdhttp.HandlerFunc(h.createAttempt), payerAuth))
	mux.Handle("POST /api/v1/payments/{bookingId}/page-tokens", httpx.Chain(stdhttp.HandlerFunc(h.createPageToken), h.auth.VisitorOnly()))
	mux.HandleFunc("POST /api/v1/payment-page-sessions", h.exchangePageToken)
}

func noStore(w stdhttp.ResponseWriter) { w.Header().Set("Cache-Control", "no-store") }

func payer(r *stdhttp.Request) inbound.Payer {
	p, _ := PayerFrom(r.Context()) // set by PayerAuth; the zero value is rejected by the usecase as not found
	return p
}

func (h *PaymentHandler) getPage(w stdhttp.ResponseWriter, r *stdhttp.Request) {
	res, err := h.page.GetPage(r.Context(), payer(r), r.PathValue("bookingId"))
	if err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	noStore(w)
	httpx.WriteData(w, r, stdhttp.StatusOK, res)
}

func (h *PaymentHandler) getStatus(w stdhttp.ResponseWriter, r *stdhttp.Request) {
	res, err := h.status.GetStatus(r.Context(), payer(r), r.PathValue("bookingId"))
	if err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	noStore(w)
	httpx.WriteData(w, r, stdhttp.StatusOK, res)
}

func (h *PaymentHandler) createAttempt(w stdhttp.ResponseWriter, r *stdhttp.Request) {
	var req dto.CreateAttemptRequest
	if err := httpx.DecodeJSON(w, r, &req, 0); err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	res, err := h.attempt.CreateAttempt(r.Context(), payer(r), r.PathValue("bookingId"), r.Header.Get("Idempotency-Key"), req)
	if err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	noStore(w)
	httpx.WriteData(w, r, stdhttp.StatusCreated, res)
}

func (h *PaymentHandler) createPageToken(w stdhttp.ResponseWriter, r *stdhttp.Request) {
	res, err := h.createToken.CreatePageToken(r.Context(), payer(r), r.PathValue("bookingId"))
	if err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	noStore(w)
	httpx.WriteData(w, r, stdhttp.StatusCreated, res)
}

func (h *PaymentHandler) exchangePageToken(w stdhttp.ResponseWriter, r *stdhttp.Request) {
	var req dto.PageSessionRequest
	if err := httpx.DecodeJSON(w, r, &req, 0); err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	s, err := h.exchange.ExchangePageToken(r.Context(), req.PageToken)
	if err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	stdhttp.SetCookie(w, &stdhttp.Cookie{
		Name: CookiePay, Value: s.Cookie, Path: "/", Expires: s.ExpiresAt,
		HttpOnly: true, Secure: h.secureCookie, SameSite: stdhttp.SameSiteLaxMode,
	})
	noStore(w)
	w.WriteHeader(stdhttp.StatusNoContent)
}
