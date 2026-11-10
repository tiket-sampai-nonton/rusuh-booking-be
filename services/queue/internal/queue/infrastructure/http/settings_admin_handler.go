package http

import (
	"context"
	stdhttp "net/http"

	"github.com/tiket-sampai-nonton/rusuh-booking-be/pkg/apperror"
	"github.com/tiket-sampai-nonton/rusuh-booking-be/pkg/httpx"

	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/queue/internal/queue/domain/dto"
	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/queue/internal/queue/domain/valueobject"
	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/queue/internal/queue/port/inbound"
	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/queue/internal/queue/port/outbound"
)

// SettingsAdminHandler serves the internal admin endpoints for per-event queue settings (QSA-1, QSA-2):
// GET and PUT /internal/v1/admin/events/{eventId}/queue-settings. Internal only: the dev Caddy rule is IP-restricted
// and the public edge (CloudFront/ALB) has no route to this path.
type SettingsAdminHandler struct {
	get      inbound.IGetQueueSettingsUsecase
	set      inbound.ISetQueueSettingsUsecase
	verifier outbound.IAdminTokenVerifier
}

// NewSettingsAdminHandler wires the handler. A nil verifier (no User Service key configured) denies every request:
// the guard fails closed and never falls back to an open endpoint.
func NewSettingsAdminHandler(get inbound.IGetQueueSettingsUsecase, set inbound.ISetQueueSettingsUsecase, verifier outbound.IAdminTokenVerifier) *SettingsAdminHandler {
	return &SettingsAdminHandler{get: get, set: set, verifier: verifier}
}

// Register mounts the routes.
func (h *SettingsAdminHandler) Register(mux *stdhttp.ServeMux) {
	mux.Handle("GET /internal/v1/admin/events/{eventId}/queue-settings", h.admin(h.getSettings))
	mux.Handle("PUT /internal/v1/admin/events/{eventId}/queue-settings", h.admin(h.putSettings))
}

type actorKey struct{}

func actorFrom(ctx context.Context) string {
	a, _ := ctx.Value(actorKey{}).(string)
	return a
}

// denyAll rejects every request with 401; used when no admin verifier is configured.
var denyAll = stdhttp.HandlerFunc(func(w stdhttp.ResponseWriter, r *stdhttp.Request) {
	w.Header().Set("WWW-Authenticate", `Bearer realm="rbt"`)
	httpx.WriteError(w, r, inbound.ErrUnauthorized)
})

// admin authenticates first (before the path or the body is looked at). Every failure is 401, never 403, so the
// response does not reveal whether a token was valid but not allow-listed. The verified subject is the audit actor.
func (h *SettingsAdminHandler) admin(next stdhttp.HandlerFunc) stdhttp.Handler {
	if h.verifier == nil {
		return denyAll
	}
	return stdhttp.HandlerFunc(func(w stdhttp.ResponseWriter, r *stdhttp.Request) {
		tok, ok := httpx.BearerToken(r)
		if !ok {
			w.Header().Set("WWW-Authenticate", `Bearer realm="rbt"`)
			httpx.WriteError(w, r, inbound.ErrUnauthorized)
			return
		}
		subject, err := h.verifier.VerifyAdmin(tok)
		if err != nil || subject == "" {
			w.Header().Set("WWW-Authenticate", `Bearer realm="rbt", error="invalid_token"`)
			httpx.WriteError(w, r, inbound.ErrUnauthorized)
			return
		}
		next(w, r.WithContext(context.WithValue(r.Context(), actorKey{}, subject)))
	})
}

// eventID validates the path parameter before any usecase or database call.
func eventID(w stdhttp.ResponseWriter, r *stdhttp.Request) (string, bool) {
	id, err := valueobject.NewEventID(r.PathValue("eventId"))
	if err != nil {
		httpx.WriteError(w, r, apperror.New(apperror.CodeValidationFailed, stdhttp.StatusBadRequest, "eventId harus UUID"))
		return "", false
	}
	return id.String(), true
}

func (h *SettingsAdminHandler) getSettings(w stdhttp.ResponseWriter, r *stdhttp.Request) {
	id, ok := eventID(w, r)
	if !ok {
		return
	}
	out, err := h.get.Get(r.Context(), id)
	if err != nil {
		writeDomainError(w, r, err)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	httpx.WriteData(w, r, stdhttp.StatusOK, out)
}

func (h *SettingsAdminHandler) putSettings(w stdhttp.ResponseWriter, r *stdhttp.Request) {
	id, ok := eventID(w, r)
	if !ok {
		return
	}
	var req dto.QueueSettingsRequest
	if err := httpx.DecodeJSON(w, r, &req, 0); err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	out, created, err := h.set.Set(r.Context(), inbound.SetSettingsInput{EventID: id, Actor: actorFrom(r.Context()), Patch: req})
	if err != nil {
		writeDomainError(w, r, err)
		return
	}
	status := stdhttp.StatusOK
	if created {
		status = stdhttp.StatusCreated
	}
	w.Header().Set("Cache-Control", "no-store")
	httpx.WriteData(w, r, status, out)
}
