// Package httpx contains HTTP server plumbing shared by all services.
package httpx

import (
	"encoding/json"
	"log/slog"
	"net/http"

	"github.com/tiket-sampai-nonton/rusuh-booking-be/pkg/apperror"
)

// WriteJSON writes v as JSON with the given status.
func WriteJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(v); err != nil {
		slog.Default().Error("httpx: encode response", "error", err)
	}
}

// WriteData writes v inside the success envelope {"meta":{"code":status,"requestId":…},"data":v} (SRS §3.1.4).
func WriteData(w http.ResponseWriter, r *http.Request, status int, v any) {
	WriteJSON(w, status, apperror.Envelope{Meta: apperror.Meta{Code: status, RequestID: RequestIDFrom(r.Context())}, Data: v})
}

// WriteError writes err using the standard error envelope.
func WriteError(w http.ResponseWriter, r *http.Request, err error) {
	ae := apperror.From(err)
	if ae.HTTPStatus >= http.StatusInternalServerError {
		slog.Default().Error("request failed", "error", err, "requestId", RequestIDFrom(r.Context()))
	}
	WriteJSON(w, ae.HTTPStatus, ae.ToEnvelope(RequestIDFrom(r.Context())))
}
