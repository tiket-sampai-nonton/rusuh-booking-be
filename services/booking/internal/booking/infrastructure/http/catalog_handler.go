package http

import (
	stdhttp "net/http"

	"github.com/tiket-sampai-nonton/rusuh-booking-be/pkg/httpx"

	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/booking/internal/booking/domain/dto"
	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/booking/internal/booking/port/inbound"
)

// CatalogHandler serves the public event catalog.
type CatalogHandler struct {
	list   inbound.IEventCatalogUsecase
	detail inbound.IEventDetailUsecase
}

// NewCatalogHandler wires the handler.
func NewCatalogHandler(list inbound.IEventCatalogUsecase, detail inbound.IEventDetailUsecase) *CatalogHandler {
	return &CatalogHandler{list: list, detail: detail}
}

// Register mounts the catalog routes.
func (h *CatalogHandler) Register(mux *stdhttp.ServeMux) {
	mux.HandleFunc("GET /api/v1/events", h.listEvents)
	mux.HandleFunc("GET /api/v1/events/{eventId}", h.getEventDetail)
}

func (h *CatalogHandler) listEvents(w stdhttp.ResponseWriter, r *stdhttp.Request) {
	q := r.URL.Query()
	resp, err := h.list.ListEvents(r.Context(), dto.EventListQuery{
		Timeframe: q.Get("timeframe"),
		City:      q.Get("city"),
		Status:    q.Get("status"),
		Sort:      q.Get("sort"),
	})
	if err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	cache2s(w)
	httpx.WriteData(w, r, stdhttp.StatusOK, resp)
}

func (h *CatalogHandler) getEventDetail(w stdhttp.ResponseWriter, r *stdhttp.Request) {
	resp, err := h.detail.GetEventDetail(r.Context(), r.PathValue("eventId"))
	if err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	cache2s(w)
	httpx.WriteData(w, r, stdhttp.StatusOK, resp)
}

func cache2s(w stdhttp.ResponseWriter) { w.Header().Set("Cache-Control", "public, max-age=2") }
