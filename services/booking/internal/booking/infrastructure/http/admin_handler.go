package http

import (
	"io"
	stdhttp "net/http"

	"github.com/tiket-sampai-nonton/rusuh-booking-be/pkg/httpx"

	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/booking/internal/booking/domain/dto"
	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/booking/internal/booking/port/inbound"
)

// AdminHandler serves admin-only event management endpoints (behind the bearer token).
type AdminHandler struct {
	create   inbound.ICreateEventUsecase
	update   inbound.IUpdateEventConfigUsecase
	verifier IUserTokenVerifier
	images   inbound.IUploadEventImageUsecase
}

// NewAdminHandler wires the admin handler.
func NewAdminHandler(
	create inbound.ICreateEventUsecase,
	update inbound.IUpdateEventConfigUsecase,
	verifier IUserTokenVerifier,
) *AdminHandler {
	return &AdminHandler{create: create, update: update, verifier: verifier}
}

// WithImages enables POST /internal/v1/admin/events/{eventId}/images.
func (h *AdminHandler) WithImages(uc inbound.IUploadEventImageUsecase) *AdminHandler {
	h.images = uc
	return h
}

// Register mounts the admin routes.
func (h *AdminHandler) Register(mux *stdhttp.ServeMux) {
	mux.Handle("POST /internal/v1/admin/events", h.admin(h.createEvent))
	mux.Handle("PATCH /internal/v1/admin/events/{eventId}", h.admin(h.updateEventConfig))
	if h.images != nil {
		mux.Handle("POST /internal/v1/admin/events/{eventId}/images", h.admin(h.uploadImage))
	}
}

// admin guards admin endpoints with the bearer token.
func (h *AdminHandler) admin(next stdhttp.HandlerFunc) stdhttp.Handler {
	if h.verifier == nil {
		return denyAll // no key configured: fail closed, never open
	}
	return withUser(h.verifier, next)
}

func (h *AdminHandler) createEvent(w stdhttp.ResponseWriter, r *stdhttp.Request) {
	var req dto.CreateEventRequest
	if err := httpx.DecodeJSON(w, r, &req, 0); err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	id, err := h.create.Create(r.Context(), req)
	if err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	noStore(w)
	httpx.WriteData(w, r, stdhttp.StatusCreated, map[string]string{"eventId": id})
}

func (h *AdminHandler) updateEventConfig(w stdhttp.ResponseWriter, r *stdhttp.Request) {
	var req dto.UpdateEventConfigRequest
	if err := httpx.DecodeJSON(w, r, &req, 0); err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	if err := h.update.Update(r.Context(), r.PathValue("eventId"), req); err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	noStore(w)
	httpx.WriteData(w, r, stdhttp.StatusOK, struct{}{})
}

// uploadImage takes a multipart form with the image in field "file"; ?kind= is image (default), banner, or thumbnail.
func (h *AdminHandler) uploadImage(w stdhttp.ResponseWriter, r *stdhttp.Request) {
	r.Body = stdhttp.MaxBytesReader(w, r.Body, inbound.MaxImageBytes+(1<<20))
	file, _, err := r.FormFile("file")
	if err != nil {
		httpx.WriteError(w, r, inbound.Validation("file", "multipart field file wajib, maksimal 5 MB"))
		return
	}
	defer func() { _ = file.Close() }()
	data, err := io.ReadAll(io.LimitReader(file, inbound.MaxImageBytes+1))
	if err != nil {
		httpx.WriteError(w, r, inbound.Validation("file", "file tidak terbaca"))
		return
	}
	kind := r.URL.Query().Get("kind")
	if kind == "" {
		kind = "image"
	}
	url, err := h.images.Upload(r.Context(), r.PathValue("eventId"), kind, data)
	if err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	noStore(w)
	httpx.WriteData(w, r, stdhttp.StatusCreated, map[string]string{"url": url})
}
