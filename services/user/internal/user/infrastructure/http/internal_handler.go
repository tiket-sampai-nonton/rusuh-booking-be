package http

import (
	"context"
	"encoding/base64"
	"errors"
	stdhttp "net/http"

	"github.com/tiket-sampai-nonton/rusuh-booking-be/pkg/apperror"
	"github.com/tiket-sampai-nonton/rusuh-booking-be/pkg/httpx"

	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/user/internal/user/domain/entity"
	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/user/internal/user/port/inbound"
	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/user/internal/user/port/outbound"
)

// InternalHandler serves service-to-service endpoints on /internal/v1/*.
// Callers authenticate by forwarding the buyer's own access token so User Service can verify its own token.
type InternalHandler struct {
	accounts outbound.IAccountRepository
	verifier inbound.IAccessTokenVerifier
}

// NewInternalHandler wires the handler.
func NewInternalHandler(accounts outbound.IAccountRepository, verifier inbound.IAccessTokenVerifier) *InternalHandler {
	return &InternalHandler{accounts: accounts, verifier: verifier}
}

// Register mounts the internal routes.
func (h *InternalHandler) Register(mux *stdhttp.ServeMux) {
	mux.Handle("GET /internal/v1/accounts/{id}/attendee-nik-enc", h.requireBuyerAuth(h.getNIKEnc))
}

// getNIKEnc returns the buyer's NIK ciphertext (sealed with AAD "users.accounts.nik") so that
// Booking Service can re-seal it for its own attendee_nik_enc column.
// The caller (Booking) must forward the buyer's access token; this endpoint only returns the NIK of the
// authenticated account, not any arbitrary account.
func (h *InternalHandler) getNIKEnc(w stdhttp.ResponseWriter, r *stdhttp.Request) {
	id := r.PathValue("id")
	callerID := accountIDFrom(r.Context())
	if callerID != id {
		httpx.WriteError(w, r, inbound.ErrUnauthorized)
		return
	}
	nikEnc, err := h.accounts.GetNIKEncByID(r.Context(), id)
	if err != nil {
		if errors.Is(err, entity.ErrNotFound) {
			httpx.WriteError(w, r, apperror.New("NOT_FOUND", stdhttp.StatusNotFound, "NIK tidak ditemukan pada akun ini"))
			return
		}
		httpx.WriteError(w, r, err)
		return
	}
	if len(nikEnc) == 0 {
		httpx.WriteError(w, r, apperror.New("NOT_FOUND", stdhttp.StatusNotFound, "NIK tidak ditemukan pada akun ini"))
		return
	}
	noStore(w)
	httpx.WriteData(w, r, stdhttp.StatusOK, map[string]string{
		"nikEnc": base64.StdEncoding.EncodeToString(nikEnc),
	})
}

func (h *InternalHandler) requireBuyerAuth(next stdhttp.HandlerFunc) stdhttp.Handler {
	return stdhttp.HandlerFunc(func(w stdhttp.ResponseWriter, r *stdhttp.Request) {
		tok, ok := httpx.BearerToken(r)
		if !ok {
			w.Header().Set("WWW-Authenticate", `Bearer realm="rbt"`)
			httpx.WriteError(w, r, inbound.ErrUnauthorized)
			return
		}
		id, err := h.verifier.VerifyAccess(tok)
		if err != nil || id == "" {
			w.Header().Set("WWW-Authenticate", `Bearer realm="rbt", error="invalid_token"`)
			httpx.WriteError(w, r, inbound.ErrUnauthorized)
			return
		}
		next(w, r.WithContext(context.WithValue(r.Context(), accountKey{}, id)))
	})
}
