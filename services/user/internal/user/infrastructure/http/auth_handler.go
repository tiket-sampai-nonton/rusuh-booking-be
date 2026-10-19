package http

import (
	"context"
	stdhttp "net/http"

	"github.com/tiket-sampai-nonton/rusuh-booking-be/pkg/httpx"

	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/user/internal/user/domain/dto"
	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/user/internal/user/port/inbound"
)

// AuthHandler serves /api/v1/auth/* and /api/v1/me (kontrak: docs/contracts/openapi/user.yaml).
type AuthHandler struct {
	auth       inbound.IAuthUsecase
	profile    inbound.IProfileUsecase
	verifier   inbound.IAccessTokenVerifier
	trustProxy bool
}

// NewAuthHandler creates the handler. trustProxy=true di belakang ALB (X-Forwarded-For).
func NewAuthHandler(auth inbound.IAuthUsecase, profile inbound.IProfileUsecase, verifier inbound.IAccessTokenVerifier, trustProxy bool) *AuthHandler {
	return &AuthHandler{auth: auth, profile: profile, verifier: verifier, trustProxy: trustProxy}
}

// Register mounts the routes.
func (h *AuthHandler) Register(mux *stdhttp.ServeMux) {
	mux.HandleFunc("POST /api/v1/auth/otp/request", h.requestOTP)
	mux.HandleFunc("POST /api/v1/auth/otp/verify", h.verifyOTP)
	mux.HandleFunc("POST /api/v1/auth/refresh", h.refresh)
	mux.HandleFunc("POST /api/v1/auth/logout", h.logout)
	mux.Handle("GET /api/v1/me", h.requireAuth(h.getMe))
	mux.Handle("PATCH /api/v1/me", h.requireAuth(h.patchMe))
}

func (h *AuthHandler) client(r *stdhttp.Request) inbound.ClientInfo {
	return inbound.ClientInfo{IP: httpx.ClientIP(r, h.trustProxy), UserAgent: r.UserAgent()}
}

func noStore(w stdhttp.ResponseWriter) { w.Header().Set("Cache-Control", "no-store") }

func (h *AuthHandler) requestOTP(w stdhttp.ResponseWriter, r *stdhttp.Request) {
	var req dto.RequestOTPRequest
	if err := httpx.DecodeJSON(w, r, &req, 0); err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	resp, err := h.auth.RequestOTP(r.Context(), req, h.client(r))
	if err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	noStore(w)
	httpx.WriteData(w, r, stdhttp.StatusAccepted, resp)
}

func (h *AuthHandler) verifyOTP(w stdhttp.ResponseWriter, r *stdhttp.Request) {
	var req dto.VerifyOTPRequest
	if err := httpx.DecodeJSON(w, r, &req, 0); err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	resp, err := h.auth.VerifyOTP(r.Context(), req, h.client(r))
	if err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	noStore(w)
	httpx.WriteData(w, r, stdhttp.StatusOK, resp)
}

func (h *AuthHandler) refresh(w stdhttp.ResponseWriter, r *stdhttp.Request) {
	var req dto.RefreshRequest
	if err := httpx.DecodeJSON(w, r, &req, 0); err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	resp, err := h.auth.Refresh(r.Context(), req, h.client(r))
	if err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	noStore(w)
	httpx.WriteData(w, r, stdhttp.StatusOK, resp)
}

func (h *AuthHandler) logout(w stdhttp.ResponseWriter, r *stdhttp.Request) {
	var req dto.RefreshRequest
	if err := httpx.DecodeJSON(w, r, &req, 0); err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	if err := h.auth.Logout(r.Context(), req, h.client(r)); err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	w.WriteHeader(stdhttp.StatusNoContent)
}

func (h *AuthHandler) getMe(w stdhttp.ResponseWriter, r *stdhttp.Request) {
	resp, err := h.profile.GetProfile(r.Context(), accountIDFrom(r.Context()))
	if err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	noStore(w)
	httpx.WriteData(w, r, stdhttp.StatusOK, resp)
}

func (h *AuthHandler) patchMe(w stdhttp.ResponseWriter, r *stdhttp.Request) {
	var req dto.UpdateProfileRequest
	if err := httpx.DecodeJSON(w, r, &req, 0); err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	resp, err := h.profile.UpdateProfile(r.Context(), accountIDFrom(r.Context()), req)
	if err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	noStore(w)
	httpx.WriteData(w, r, stdhttp.StatusOK, resp)
}

type accountKey struct{}

func accountIDFrom(ctx context.Context) string {
	id, _ := ctx.Value(accountKey{}).(string)
	return id
}

// requireAuth verifies the Bearer access token and stores the account id in the context.
func (h *AuthHandler) requireAuth(next stdhttp.HandlerFunc) stdhttp.Handler {
	return stdhttp.HandlerFunc(func(w stdhttp.ResponseWriter, r *stdhttp.Request) {
		tok, ok := httpx.BearerToken(r)
		if !ok {
			w.Header().Set("WWW-Authenticate", `Bearer realm="rbt"`)
			httpx.WriteError(w, r, inbound.ErrUnauthorized)
			return
		}
		id, err := h.verifier.VerifyAccess(tok)
		if err != nil {
			w.Header().Set("WWW-Authenticate", `Bearer realm="rbt", error="invalid_token"`)
			httpx.WriteError(w, r, inbound.ErrUnauthorized)
			return
		}
		next(w, r.WithContext(context.WithValue(r.Context(), accountKey{}, id)))
	})
}
