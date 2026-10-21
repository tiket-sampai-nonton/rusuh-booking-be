package http

import (
	stdhttp "net/http"

	"github.com/tiket-sampai-nonton/rusuh-booking-be/pkg/httpx"

	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/notification/internal/notification/domain/dto"
	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/notification/internal/notification/port/inbound"
)

// VisitorAuthenticator resolves the visitor id from the request (X-Visitor-Token / cookie).
type VisitorAuthenticator interface {
	Resolve(r *stdhttp.Request) (visitorID string, ok bool)
}

// SubscriptionHandler serves the push-subscription API (kontrak notification.yaml).
type SubscriptionHandler struct {
	uc   inbound.ISubscriptionUsecase
	auth VisitorAuthenticator
}

// NewSubscriptionHandler creates the handler.
func NewSubscriptionHandler(uc inbound.ISubscriptionUsecase, auth VisitorAuthenticator) *SubscriptionHandler {
	return &SubscriptionHandler{uc: uc, auth: auth}
}

// Register mounts the routes (route registrar per domain).
func (h *SubscriptionHandler) Register(mux *stdhttp.ServeMux) {
	mux.HandleFunc("POST /api/v1/push-subscriptions", h.subscribe)
	mux.HandleFunc("DELETE /api/v1/push-subscriptions/{eventId}", h.unsubscribe)
}

func (h *SubscriptionHandler) subscribe(w stdhttp.ResponseWriter, r *stdhttp.Request) {
	visitorID, ok := h.auth.Resolve(r)
	if !ok {
		httpx.WriteError(w, r, inbound.ErrUnauthorized)
		return
	}
	var req dto.SubscribeRequest
	if err := httpx.DecodeJSON(w, r, &req, 0); err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	if err := h.uc.Subscribe(r.Context(), visitorID, req); err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	w.WriteHeader(stdhttp.StatusNoContent)
}

func (h *SubscriptionHandler) unsubscribe(w stdhttp.ResponseWriter, r *stdhttp.Request) {
	visitorID, ok := h.auth.Resolve(r)
	if !ok {
		httpx.WriteError(w, r, inbound.ErrUnauthorized)
		return
	}
	eventID := r.PathValue("eventId")
	if err := h.uc.Unsubscribe(r.Context(), visitorID, eventID); err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	w.WriteHeader(stdhttp.StatusNoContent)
}
