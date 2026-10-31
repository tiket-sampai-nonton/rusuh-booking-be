package http

import (
	stdhttp "net/http"
	"strings"

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
	// genres mendukung dua bentuk: berulang (?genres=POP&genres=ROCK) dan CSV (?genres=POP,ROCK).
	// Param skalar lain pakai Get() (nilai pertama saja) — jangan ubah ke slice untuk skalar.
	resp, err := h.list.ListEvents(r.Context(), dto.EventListQuery{
		Timeframe: q.Get("timeframe"),
		City:      q.Get("city"),
		Genres:    parseGenres(q["genres"]),
		Status:    q.Get("status"),
		Sort:      q.Get("sort"),
		StartDate: q.Get("startDate"),
		EndDate:   q.Get("endDate"),
	})
	if err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	cache2s(w)
	httpx.WriteData(w, r, stdhttp.StatusOK, resp)
}

// parseGenres flattens repeated and comma-separated genre params into raw tokens.
// Normalisation, dedupe, and caps happen in the usecase (single source of truth).
func parseGenres(raw []string) []string {
	if len(raw) == 0 {
		return nil
	}
	out := make([]string, 0, len(raw))
	for _, v := range raw {
		for _, part := range strings.Split(v, ",") {
			if p := strings.TrimSpace(part); p != "" {
				out = append(out, p)
			}
		}
	}
	return out
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
