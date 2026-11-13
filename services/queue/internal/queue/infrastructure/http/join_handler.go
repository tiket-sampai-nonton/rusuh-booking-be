package http

import (
	"io"
	stdhttp "net/http"
	"strings"

	"github.com/tiket-sampai-nonton/rusuh-booking-be/pkg/httpx"

	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/queue/internal/queue/domain/dto"
	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/queue/internal/queue/port/inbound"
)

// JoinHandler serves POST /api/v1/events/{eventId}/queue-entries (design §2.1).
type JoinHandler struct {
	uc         inbound.IJoinUsecase
	auth       VisitorTokenAuthenticator // verifies the visitor token (id, generation, device claim)
	trustProxy bool
	hops       int // trusted proxies that append X-Forwarded-For entries (APP_TRUSTED_PROXY_HOPS)
}

// NewJoinHandler creates the handler. trustProxy=true behind the load balancer (X-Forwarded-For); hops is the number of
// trusted proxies that each append one entry (the client IP is the entry hops from the right). The visitor cookie is
// always Secure (https; browsers also accept it on localhost); plain-http clients send X-Client-Type: mobile and keep
// the token.
func NewJoinHandler(uc inbound.IJoinUsecase, trustProxy bool, hops int) *JoinHandler {
	return &JoinHandler{uc: uc, trustProxy: trustProxy, hops: hops}
}

// WithAuth sets the visitor authenticator (call this to enable JWT decoding on join).
func (h *JoinHandler) WithAuth(auth VisitorTokenAuthenticator) *JoinHandler {
	h.auth = auth
	return h
}

// Register mounts the route.
func (h *JoinHandler) Register(mux *stdhttp.ServeMux) {
	mux.HandleFunc("POST /api/v1/events/{eventId}/queue-entries", h.join)
}

func (h *JoinHandler) join(w stdhttp.ResponseWriter, r *stdhttp.Request) {
	// The join is anonymous (ADR-030): the body is not a contract. A legacy client may still send
	// buyerNik/email/consent; the bytes are drained (bounded) and discarded, never parsed, stored, logged or echoed.
	var body dto.JoinRequest
	if r.Body != nil {
		_, _ = io.Copy(io.Discard, stdhttp.MaxBytesReader(w, r.Body, 4096))
	}
	bearer, _ := httpx.BearerToken(r)
	// Resolve visitor ID: ONLY a verified JWT subject is used as the visitor id (TC-QUE-29, INV-3). The raw
	// cookie/header value is never trusted directly — using it as a fallback let a forged X-Visitor-ID or
	// rb_visitor cookie impersonate another visitor's queue identity (VISITOR_ALREADY_IN_QUEUE for the attacker,
	// or an entry keyed to the victim's visitor id). No credential at all simply means a fresh visitor id is minted.
	vid, gen, device, guest := "", int64(0), "", ""
	cookieAuthed := false
	clientIP := httpx.ClientIPHops(r, h.trustProxy, h.hops)
	if h.auth != nil {
		if c, ok := h.auth.ResolveToken(r); ok {
			vid, gen = c.VisitorID, c.Gen
			// ResolveToken reads a non-empty cookie exclusively, so ok + non-empty cookie means the cookie verified.
			cookieAuthed = cookiePresent(r)
		}
		device = h.auth.DeviceHash(r) // only the HMAC reaches the usecase and the token, never the raw id
		guest = h.auth.GuestScope(r, clientIP)
	}
	in := inbound.JoinInput{
		EventID:        r.PathValue("eventId"),
		Body:           body,
		IdempotencyKey: r.Header.Get("Idempotency-Key"),
		TokenGen:       gen,
		DeviceHash:     device,
		GuestScope:     guest,
		Bearer:         bearer,
		VisitorID:      vid,
		IsMobile:       isMobile(r),
		ClientIP:       clientIP,
	}
	bodyToken := in.IsMobile && !cookieAuthed
	resp, err := h.uc.Join(r.Context(), in)
	if err != nil {
		writeDomainError(w, r, err)
		return
	}
	// The current-generation token is ALWAYS delivered in the HttpOnly cookie. It is echoed in the body only to a
	// native client (X-Client-Type: mobile) that did not authenticate with a valid cookie: a cookie holder (browser,
	// WebView) must never get a script-readable copy, even when it spoofs the mobile header.
	if resp.VisitorToken != "" {
		stdhttp.SetCookie(w, &stdhttp.Cookie{
			Name: cookieVisitor, Value: resp.VisitorToken, Path: "/",
			HttpOnly: true, Secure: true, SameSite: httpx.CookieSameSite(),
		})
		if !bodyToken {
			resp.VisitorToken = ""
		}
	}
	w.Header().Set("Cache-Control", "no-store")
	status := stdhttp.StatusCreated
	if resp.Existing {
		status = stdhttp.StatusOK // an already-active entry is returned idempotently
	}
	httpx.WriteData(w, r, status, resp)
}

// isMobile reports whether the client declares itself a native app (X-Client-Type: mobile).
func isMobile(r *stdhttp.Request) bool {
	return strings.EqualFold(r.Header.Get("X-Client-Type"), "mobile")
}

// cookiePresent reports whether the request carries a non-empty rb_visitor cookie.
func cookiePresent(r *stdhttp.Request) bool {
	c, err := r.Cookie(cookieVisitor)
	return err == nil && c.Value != ""
}
