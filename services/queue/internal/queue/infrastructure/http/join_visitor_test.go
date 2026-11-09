package http_test

import (
	"bytes"
	"crypto/ed25519"
	"encoding/json"
	"log/slog"
	stdhttp "net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	"github.com/tiket-sampai-nonton/rusuh-booking-be/pkg/token"
	mock_inbound "github.com/tiket-sampai-nonton/rusuh-booking-be/services/queue/_mock/queue/port/inbound"
	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/queue/internal/queue/domain/dto"
	queuehttp "github.com/tiket-sampai-nonton/rusuh-booking-be/services/queue/internal/queue/infrastructure/http"
	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/queue/internal/queue/infrastructure/security"
	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/queue/internal/queue/port/inbound"
	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/queue/internal/queue/port/outbound"
)

const joinBody = `{"buyerNik":"3201234567890123","email":"a@b.id","emailConfirmation":"a@b.id","consentNik":true}`

func joinVisitorIDSeen(t *testing.T, auth queuehttp.VisitorTokenAuthenticator, mutate func(*stdhttp.Request)) string {
	t.Helper()
	ctrl := gomock.NewController(t)
	uc := mock_inbound.NewMockIJoinUsecase(ctrl)
	var seen string
	uc.EXPECT().Join(gomock.Any(), gomock.Any()).DoAndReturn(func(_ interface{ Done() <-chan struct{} }, in inbound.JoinInput) (dto.QueueEntry, error) {
		seen = in.VisitorID
		return dto.QueueEntry{EntryID: "e1"}, nil
	})
	h := queuehttp.NewJoinHandler(uc, false)
	if auth != nil {
		h = h.WithAuth(auth)
	}
	mux := stdhttp.NewServeMux()
	h.Register(mux)
	req := httptest.NewRequest(stdhttp.MethodPost, "/api/v1/events/ev1/queue-entries", strings.NewReader(joinBody))
	req.Header.Set("Content-Type", "application/json")
	mutate(req)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	require.Equal(t, stdhttp.StatusCreated, rec.Code)
	return seen
}

// TC-QUE-29 (INV-3): an unverified visitor id from a cookie or header is never used. Before the fix a forged
// X-Visitor-ID of a victim's visitor id made join answer VISITOR_ALREADY_IN_QUEUE for the attacker and, worse, could
// create an entry owned by the victim's visitor id.
func TestJoinHandler_ForgedVisitorHeaderIsNeverUsed(t *testing.T) {
	seen := joinVisitorIDSeen(t, fakeAuth{id: "", ok: false}, func(r *stdhttp.Request) { r.Header.Set("X-Visitor-ID", "victim-visitor-uuid") })
	assert.Empty(t, seen)
}

func TestJoinHandler_ForgedVisitorCookieIsNeverUsed(t *testing.T) {
	seen := joinVisitorIDSeen(t, fakeAuth{id: "", ok: false}, func(r *stdhttp.Request) {
		r.AddCookie(&stdhttp.Cookie{Name: "rb_visitor", Value: "forged.jwt.value"})
	})
	assert.Empty(t, seen)
}

func TestJoinHandler_HeaderIsIgnoredWithoutAnAuthenticator(t *testing.T) {
	seen := joinVisitorIDSeen(t, nil, func(r *stdhttp.Request) { r.Header.Set("X-Visitor-ID", "victim-visitor-uuid") })
	assert.Empty(t, seen, "tanpa authenticator, id yang tak terverifikasi tidak dipakai")
}

func TestJoinHandler_VerifiedVisitorTokenIsUsed(t *testing.T) {
	seen := joinVisitorIDSeen(t, fakeAuth{id: "visitor-sub", ok: true}, func(r *stdhttp.Request) {
		r.AddCookie(&stdhttp.Cookie{Name: "rb_visitor", Value: "valid.jwt"})
	})
	assert.Equal(t, "visitor-sub", seen)
}

func TestJoinHandler_NoVisitorCredentialStillJoins(t *testing.T) {
	seen := joinVisitorIDSeen(t, fakeAuth{id: "", ok: false}, func(*stdhttp.Request) {})
	assert.Empty(t, seen)
}

// QSH-OQ-9 (XFF review): behind the ALB the LAST X-Forwarded-For element is the one the proxy appended, so it is the
// only one a client cannot forge; without trustProxy the socket address is used and X-Forwarded-For is ignored.
// pkg/httpx.ClientIP implements exactly that; this pins that join passes it through unchanged.
func TestJoinHandler_ClientIPTrustSemantics(t *testing.T) {
	for _, tc := range []struct {
		name       string
		trustProxy bool
		want       string
	}{
		{"behind the ALB: last XFF element (appended by the proxy)", true, "203.0.113.9"},
		{"local/no proxy: socket address, XFF ignored", false, "192.0.2.1"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctrl := gomock.NewController(t)
			uc := mock_inbound.NewMockIJoinUsecase(ctrl)
			var seen string
			uc.EXPECT().Join(gomock.Any(), gomock.Any()).DoAndReturn(func(_ interface{ Done() <-chan struct{} }, in inbound.JoinInput) (dto.QueueEntry, error) {
				seen = in.ClientIP
				return dto.QueueEntry{EntryID: "e1"}, nil
			})
			h := queuehttp.NewJoinHandler(uc, tc.trustProxy)
			mux := stdhttp.NewServeMux()
			h.Register(mux)
			req := httptest.NewRequest(stdhttp.MethodPost, "/api/v1/events/ev1/queue-entries", strings.NewReader(joinBody))
			req.Header.Set("Content-Type", "application/json")
			req.Header.Set("X-Forwarded-For", "6.6.6.6, 203.0.113.9") // client-forged first, proxy-appended last
			req.RemoteAddr = "192.0.2.1:4242"
			rec := httptest.NewRecorder()
			mux.ServeHTTP(rec, req)
			require.Equal(t, stdhttp.StatusCreated, rec.Code)
			assert.Equal(t, tc.want, seen)
		})
	}
}

// ADR-030: the join passes the token generation and only the HMAC of X-Device-Id (never the raw id) to the usecase,
// and an already-active entry is answered 200 (201 only for a new entry).
func TestJoinHandler_PassesGenerationAndDeviceHashAndAnswers200ForExisting(t *testing.T) {
	ctrl := gomock.NewController(t)
	uc := mock_inbound.NewMockIJoinUsecase(ctrl)
	var in inbound.JoinInput
	uc.EXPECT().Join(gomock.Any(), gomock.Any()).DoAndReturn(func(_ interface{ Done() <-chan struct{} }, got inbound.JoinInput) (dto.QueueEntry, error) {
		in = got
		return dto.QueueEntry{EntryID: "e1", Existing: true}, nil
	})
	h := queuehttp.NewJoinHandler(uc, false).WithAuth(fakeAuth{id: "v", ok: true})
	mux := stdhttp.NewServeMux()
	h.Register(mux)
	req := httptest.NewRequest(stdhttp.MethodPost, "/api/v1/events/ev1/queue-entries", nil)
	req.Header.Set("X-Device-Id", "raw-device-id")
	req.Header.Set("Idempotency-Key", "idem-0123456789abcdef")
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	assert.Equal(t, stdhttp.StatusOK, rec.Code)
	assert.EqualValues(t, 1, in.TokenGen)
	assert.Equal(t, "dh-of-device", in.DeviceHash)
	assert.NotContains(t, in.DeviceHash, "raw-device-id")
	assert.Equal(t, "idem-0123456789abcdef", in.IdempotencyKey)
}

// tokenRig wires the REAL VisitorAuthenticator/TokenIssuer so the join handler is exercised against genuine tokens.
type tokenRig struct {
	iss  *security.TokenIssuer
	auth *security.VisitorAuthenticator
	now  time.Time
}

func newTokenRig(t *testing.T) *tokenRig {
	t.Helper()
	pub, priv, err := ed25519.GenerateKey(nil)
	require.NoError(t, err)
	signer, err := token.NewSigner(priv, "queue-1")
	require.NoError(t, err)
	verifier, err := token.NewVerifier(map[string]ed25519.PublicKey{"queue-1": pub}, "rbt-queue", "rbt", 5*time.Second)
	require.NoError(t, err)
	now := time.Now()
	return &tokenRig{
		iss:  security.NewTokenIssuer(signer, "rbt-queue", "rbt", time.Minute, 24*time.Hour, time.Hour),
		auth: security.NewVisitorAuthenticator(verifier, func() time.Time { return now }),
		now:  now,
	}
}

func (g *tokenRig) mint(t *testing.T, vid string) string {
	t.Helper()
	tok, err := g.iss.IssueVisitor(outbound.VisitorClaim{VisitorID: vid, Gen: 1}, g.now)
	require.NoError(t, err)
	return tok
}

// join performs one POST against a join handler whose usecase always issues a fresh current-generation token. It
// returns the recorder and the visitor id the usecase received.
func (g *tokenRig) join(t *testing.T, mutate func(*stdhttp.Request)) (*httptest.ResponseRecorder, string) {
	t.Helper()
	ctrl := gomock.NewController(t)
	uc := mock_inbound.NewMockIJoinUsecase(ctrl)
	var seen string
	uc.EXPECT().Join(gomock.Any(), gomock.Any()).DoAndReturn(func(_ interface{ Done() <-chan struct{} }, in inbound.JoinInput) (dto.QueueEntry, error) {
		seen = in.VisitorID
		vid := in.VisitorID
		if vid == "" {
			vid = "minted-visitor"
		}
		return dto.QueueEntry{EntryID: "e1", Status: "WAITING", VisitorToken: g.mint(t, vid)}, nil
	})
	mux := stdhttp.NewServeMux()
	queuehttp.NewJoinHandler(uc, false).WithAuth(g.auth).Register(mux)
	req := httptest.NewRequest(stdhttp.MethodPost, "/api/v1/events/ev1/queue-entries", strings.NewReader(joinBody))
	req.Header.Set("Content-Type", "application/json")
	mutate(req)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	return rec, seen
}

func bodyVisitorToken(t *testing.T, rec *httptest.ResponseRecorder) string {
	t.Helper()
	var env struct {
		Data struct {
			VisitorToken string `json:"visitorToken"`
			EntryID      string `json:"entryId"`
			Status       string `json:"status"`
		} `json:"data"`
	}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &env))
	assert.Equal(t, "e1", env.Data.EntryID)
	assert.Equal(t, "WAITING", env.Data.Status)
	return env.Data.VisitorToken
}

func visitorCookie(rec *httptest.ResponseRecorder) *stdhttp.Cookie {
	for _, c := range rec.Result().Cookies() {
		if c.Name == "rb_visitor" {
			return c
		}
	}
	return nil
}

// TC-QUE-41 (REQ-QVT-01, P1/P2): the body carries visitorToken iff the caller is mobile AND holds no valid cookie;
// the HttpOnly cookie is set whenever a token is issued. M1-M10 from the design.
func TestJoinHandler_VisitorTokenDeliveryMatrix(t *testing.T) {
	g := newTokenRig(t)
	valid := g.mint(t, "known-visitor")
	tests := []struct {
		name         string
		mutate       func(*stdhttp.Request)
		wantBody     bool
		wantSeenVID  string
		wantCookieOf string // visitor id inside the cookie token ("" = new/minted)
	}{
		{"M1 web, no credential", func(*stdhttp.Request) {}, false, "", "minted-visitor"},
		{"M2 web, valid cookie", func(r *stdhttp.Request) { r.AddCookie(&stdhttp.Cookie{Name: "rb_visitor", Value: valid}) }, false, "known-visitor", "known-visitor"},
		{"M3 mobile, valid cookie", func(r *stdhttp.Request) {
			r.Header.Set("X-Client-Type", "mobile")
			r.AddCookie(&stdhttp.Cookie{Name: "rb_visitor", Value: valid})
		}, false, "known-visitor", "known-visitor"},
		{"M4 MOBILE (case-insensitive), valid cookie + valid header", func(r *stdhttp.Request) {
			r.Header.Set("X-Client-Type", "MOBILE")
			r.Header.Set("X-Visitor-Token", valid)
			r.AddCookie(&stdhttp.Cookie{Name: "rb_visitor", Value: valid})
		}, false, "known-visitor", "known-visitor"},
		{"M5 mobile, invalid non-empty cookie + valid header", func(r *stdhttp.Request) {
			r.Header.Set("X-Client-Type", "mobile")
			r.Header.Set("X-Visitor-Token", valid)
			r.AddCookie(&stdhttp.Cookie{Name: "rb_visitor", Value: "garbage"})
		}, true, "", "minted-visitor"},
		{"M6 web, invalid cookie + valid header", func(r *stdhttp.Request) {
			r.Header.Set("X-Visitor-Token", valid)
			r.AddCookie(&stdhttp.Cookie{Name: "rb_visitor", Value: "garbage"})
		}, false, "", "minted-visitor"},
		{"M7 mobile, no credential", func(r *stdhttp.Request) { r.Header.Set("X-Client-Type", "mobile") }, true, "", "minted-visitor"},
		{"M8 mobile, valid header only", func(r *stdhttp.Request) {
			r.Header.Set("X-Client-Type", "mobile")
			r.Header.Set("X-Visitor-Token", valid)
		}, true, "known-visitor", "known-visitor"},
		{"M9 web, valid header only", func(r *stdhttp.Request) { r.Header.Set("X-Visitor-Token", valid) }, false, "known-visitor", "known-visitor"},
		{"M10 other client type, valid cookie", func(r *stdhttp.Request) {
			r.Header.Set("X-Client-Type", "web")
			r.AddCookie(&stdhttp.Cookie{Name: "rb_visitor", Value: valid})
		}, false, "known-visitor", "known-visitor"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var logs bytes.Buffer
			prev := slog.Default()
			slog.SetDefault(slog.New(slog.NewTextHandler(&logs, nil)))
			t.Cleanup(func() { slog.SetDefault(prev) })

			rec, seen := g.join(t, tt.mutate)
			require.Equal(t, stdhttp.StatusCreated, rec.Code)
			assert.Equal(t, tt.wantSeenVID, seen)

			bodyTok := bodyVisitorToken(t, rec)
			assert.Equal(t, tt.wantBody, bodyTok != "", "body visitorToken presence")

			c := visitorCookie(rec)
			require.NotNil(t, c, "cookie is set whenever a token is issued")
			assert.True(t, c.HttpOnly)
			assert.True(t, c.Secure)
			assert.Equal(t, stdhttp.SameSiteLaxMode, c.SameSite)
			assert.Equal(t, "/", c.Path)
			cl, ok := g.auth.ResolveToken(func() *stdhttp.Request {
				r := httptest.NewRequest(stdhttp.MethodGet, "/", nil)
				r.AddCookie(c)
				return r
			}())
			require.True(t, ok)
			assert.Equal(t, tt.wantCookieOf, cl.VisitorID)

			assert.NotContains(t, logs.String(), c.Value, "token never logged")
		})
	}
}

// TC-QUE-41 criterion 8: a web join sets the cookie; replaying it with a spoofed X-Client-Type: mobile must not
// reveal the token in the body (the cookie is the credential, script cannot read it).
func TestJoinHandler_SpoofedMobileHeaderWithCookieNeverLeaksToken(t *testing.T) {
	g := newTokenRig(t)
	rec, _ := g.join(t, func(*stdhttp.Request) {})
	require.Empty(t, bodyVisitorToken(t, rec))
	c := visitorCookie(rec)
	require.NotNil(t, c)

	rec2, seen := g.join(t, func(r *stdhttp.Request) {
		r.AddCookie(c)
		r.Header.Set("X-Client-Type", "mobile")
	})
	assert.Equal(t, "minted-visitor", seen)
	assert.Empty(t, bodyVisitorToken(t, rec2))
	assert.NotNil(t, visitorCookie(rec2))
}
