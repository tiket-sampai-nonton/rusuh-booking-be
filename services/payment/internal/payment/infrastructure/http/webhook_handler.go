package http

import (
	"encoding/json"
	"errors"
	"io"
	stdhttp "net/http"

	"github.com/tiket-sampai-nonton/rusuh-booking-be/pkg/apperror"
	"github.com/tiket-sampai-nonton/rusuh-booking-be/pkg/httpx"

	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/payment/internal/payment/domain/dto"
	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/payment/internal/payment/port/inbound"
)

// maxWebhookBody bounds a Midtrans notification (a few KB in practice).
const maxWebhookBody = 64 << 10

// WebhookHandler serves POST /api/v1/webhooks/midtrans. It has no session: the signature inside the body is
// the credential, verified by the usecase.
type WebhookHandler struct{ webhook inbound.IHandleWebhook }

// NewWebhookHandler wires the handler.
func NewWebhookHandler(w inbound.IHandleWebhook) *WebhookHandler { return &WebhookHandler{webhook: w} }

// Register mounts the route.
func (h *WebhookHandler) Register(mux *stdhttp.ServeMux) {
	mux.HandleFunc("POST /api/v1/webhooks/midtrans", h.handle)
}

func (h *WebhookHandler) handle(w stdhttp.ResponseWriter, r *stdhttp.Request) {
	raw, err := io.ReadAll(stdhttp.MaxBytesReader(w, r.Body, maxWebhookBody))
	if err != nil {
		httpx.WriteError(w, r, apperror.New(apperror.CodeValidationFailed, stdhttp.StatusRequestEntityTooLarge, "body terlalu besar"))
		return
	}
	// Midtrans may add fields (additionalProperties), so unknown fields are ignored here, unlike other endpoints.
	var n dto.MidtransNotification
	if err := json.Unmarshal(raw, &n); err != nil {
		httpx.WriteError(w, r, apperror.New(apperror.CodeValidationFailed, stdhttp.StatusBadRequest, "body JSON tidak valid"))
		return
	}
	if err := h.webhook.HandleWebhook(r.Context(), n, raw); err != nil {
		var ae *apperror.Error
		if errors.As(err, &ae) && ae.Code == inbound.CodeSignatureInvalid {
			w.Header().Set("WWW-Authenticate", "Signature")
		}
		httpx.WriteError(w, r, err)
		return
	}
	httpx.WriteData(w, r, stdhttp.StatusOK, nil)
}
