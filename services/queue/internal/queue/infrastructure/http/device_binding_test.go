package http_test

import (
	"bytes"
	"crypto/ed25519"
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

const rawDevice = "raw-device-id-SECRET-123"

type deviceRig struct {
	auth     *security.VisitorAuthenticator
	verifier *token.Verifier
	hasher   *security.DeviceHasher
	bound    string // token bound to rawDevice
	unbound  string // token without a device claim
	now      time.Time
	expired  string
}

func newDeviceRig(t *testing.T) *deviceRig {
	t.Helper()
	pub, priv, err := ed25519.GenerateKey(nil)
	require.NoError(t, err)
	signer, err := token.NewSigner(priv, "queue-1")
	require.NoError(t, err)
	verifier, err := token.NewVerifier(map[string]ed25519.PublicKey{"queue-1": pub}, "rbt-queue", "rbt", time.Second)
	require.NoError(t, err)
	key := make([]byte, 32)
	for i := range key {
		key[i] = byte(i + 1)
	}
	dh, err := security.NewDeviceHasher(key)
	require.NoError(t, err)
	now := time.Date(2026, 10, 20, 10, 0, 0, 0, time.UTC)
	iss := security.NewTokenIssuer(signer, "rbt-queue", "rbt", time.Minute, 24*time.Hour, time.Hour)
	mint := func(c outbound.VisitorClaim, at time.Time) string {
		tok, err := iss.IssueVisitor(c, at)
		require.NoError(t, err)
		return tok
	}
	return &deviceRig{
		verifier: verifier,
		hasher:   dh,
		auth:     security.NewVisitorAuthenticator(verifier, func() time.Time { return now }).WithDeviceHasher(dh),
		bound:    mint(outbound.VisitorClaim{VisitorID: "v1", Gen: 1, DeviceHash: dh.Hash(rawDevice)}, now),
		unbound:  mint(outbound.VisitorClaim{VisitorID: "v1", Gen: 1}, now),
		now:      now,
		expired:  mint(outbound.VisitorClaim{VisitorID: "v1", Gen: 1}, now.Add(-48*time.Hour)),
	}
}

// C6.2 (P1-13): a tokenless guest's idempotency scope is the device hash when X-Device-Id is sent, else a keyed hash
// of the client IP ("ip-" prefix). The raw device id and the raw IP never reach the usecase.
func TestJoinHandler_GuestScopeFromDeviceOrIP(t *testing.T) {
	g := newDeviceRig(t)
	const remoteIP = "203.0.113.7"
	join := func(t *testing.T, device string) inbound.JoinInput {
		t.Helper()
		ctrl := gomock.NewController(t)
		uc := mock_inbound.NewMockIJoinUsecase(ctrl)
		var in inbound.JoinInput
		uc.EXPECT().Join(gomock.Any(), gomock.Any()).DoAndReturn(func(_ interface{ Done() <-chan struct{} }, got inbound.JoinInput) (dto.QueueEntry, error) {
			in = got
			return dto.QueueEntry{EntryID: "e1"}, nil
		})
		mux := stdhttp.NewServeMux()
		queuehttp.NewJoinHandler(uc, false, 1).WithAuth(g.auth).Register(mux)
		req := httptest.NewRequest(stdhttp.MethodPost, "/api/v1/events/ev1/queue-entries", strings.NewReader("{}"))
		req.RemoteAddr = remoteIP + ":4321"
		if device != "" {
			req.Header.Set("X-Device-Id", device)
		}
		mux.ServeHTTP(httptest.NewRecorder(), req)
		return in
	}

	t.Run("X-Device-Id scopes by the device hash", func(t *testing.T) {
		in := join(t, rawDevice)
		assert.Equal(t, remoteIP, in.ClientIP)
		assert.Equal(t, g.hasher.Hash(rawDevice), in.GuestScope)
		assert.NotContains(t, in.GuestScope, rawDevice)
	})

	t.Run("without X-Device-Id scopes by a keyed hash of the IP", func(t *testing.T) {
		in := join(t, "")
		assert.Regexp(t, `^ip-[0-9a-f]{32}$`, in.GuestScope)
		assert.NotContains(t, in.GuestScope, remoteIP)
		assert.Equal(t, in.GuestScope, join(t, "").GuestScope, "stable for the same IP")
	})
}

// authAt returns an authenticator for the same keys whose clock is fixed at t.
func (g *deviceRig) authAt(t time.Time) *security.VisitorAuthenticator {
	return security.NewVisitorAuthenticator(g.verifier, func() time.Time { return t }).WithDeviceHasher(g.hasher)
}

type deviceRoute struct {
	name   string
	method string
	path   string
	mount  func(ctrl *gomock.Controller, auth *security.VisitorAuthenticator, mux *stdhttp.ServeMux)
	okCode int
}

func deviceRoutes() []deviceRoute {
	return []deviceRoute{
		{"me", stdhttp.MethodGet, "/api/v1/queue-entries/me?eventId=5b1f2c9e-0000-4000-8000-000000000001", func(c *gomock.Controller, a *security.VisitorAuthenticator, m *stdhttp.ServeMux) {
			uc := mock_inbound.NewMockIMyEntryUsecase(c)
			uc.EXPECT().GetMyEntry(gomock.Any(), gomock.Any()).Return(dto.EntryStatus{EntryID: "e1"}, nil).AnyTimes()
			queuehttp.NewMyEntryHandler(uc, a).Register(m)
		}, stdhttp.StatusOK},
		{"heartbeat", stdhttp.MethodPost, "/api/v1/queue-entries/me/heartbeat?eventId=ev1", func(c *gomock.Controller, a *security.VisitorAuthenticator, m *stdhttp.ServeMux) {
			uc := mock_inbound.NewMockIHeartbeatUsecase(c)
			uc.EXPECT().Heartbeat(gomock.Any(), gomock.Any()).Return(dto.HeartbeatStatus{}, nil).AnyTimes()
			queuehttp.NewHeartbeatHandler(uc, a).Register(m)
		}, stdhttp.StatusOK},
		{"claim", stdhttp.MethodPost, "/api/v1/queue-entries/e1/claim?eventId=5b1f2c9e-0000-4000-8000-000000000001", func(c *gomock.Controller, a *security.VisitorAuthenticator, m *stdhttp.ServeMux) {
			uc := mock_inbound.NewMockIClaimUsecase(c)
			uc.EXPECT().Claim(gomock.Any(), gomock.Any()).Return(dto.ClaimResponse{AdmissionToken: "adm"}, nil).AnyTimes()
			queuehttp.NewClaimHandler(uc, a).Register(m)
		}, stdhttp.StatusOK},
		{"leave", stdhttp.MethodDelete, "/api/v1/queue-entries/e1?eventId=5b1f2c9e-0000-4000-8000-000000000001", func(c *gomock.Controller, a *security.VisitorAuthenticator, m *stdhttp.ServeMux) {
			uc := mock_inbound.NewMockILeaveUsecase(c)
			uc.EXPECT().Leave(gomock.Any(), gomock.Any()).Return(nil).AnyTimes()
			queuehttp.NewLeaveHandler(uc, a).Register(m)
		}, stdhttp.StatusNoContent},
	}
}

// TC-QUE-45 (REQ-QVT-04.3-.5, P11/P12): a device-bound token is refused (401 VISITOR_TOKEN_INVALID, usecase never
// reached for an invalid caller) on me/heartbeat/claim/leave when X-Device-Id is absent or different; an unbound token
// works with or without the header; neither the response nor the logs ever contain the raw device id.
func TestDeviceBinding_PerRouteMatrix(t *testing.T) {
	g := newDeviceRig(t)
	cases := []struct {
		name    string
		token   string
		device  string // "" = header absent
		want401 bool
	}{
		{"bound token, no header", g.bound, "", true},
		{"bound token, other device", g.bound, "some-other-device", true},
		{"bound token, matching device", g.bound, rawDevice, false},
		{"unbound token, no header", g.unbound, "", false},
		{"unbound token, any header", g.unbound, "whatever-device", false},
		{"expired token", g.expired, "", true},
	}
	for _, rt := range deviceRoutes() {
		for _, tc := range cases {
			t.Run(rt.name+"/"+tc.name, func(t *testing.T) {
				var logs bytes.Buffer
				prev := slog.Default()
				slog.SetDefault(slog.New(slog.NewTextHandler(&logs, nil)))
				t.Cleanup(func() { slog.SetDefault(prev) })

				ctrl := gomock.NewController(t)
				mux := stdhttp.NewServeMux()
				rt.mount(ctrl, g.auth, mux)
				req := httptest.NewRequest(rt.method, rt.path, nil)
				req.Header.Set("X-Visitor-Token", tc.token)
				if tc.device != "" {
					req.Header.Set("X-Device-Id", tc.device)
				}
				rec := httptest.NewRecorder()
				mux.ServeHTTP(rec, req)

				if tc.want401 {
					require.Equal(t, stdhttp.StatusUnauthorized, rec.Code)
					assert.Contains(t, rec.Body.String(), "VISITOR_TOKEN_INVALID")
				} else {
					require.Equal(t, rt.okCode, rec.Code)
				}
				for _, s := range []string{rec.Body.String(), logs.String(), rec.Header().Get("Set-Cookie")} {
					assert.NotContains(t, s, rawDevice)
				}
			})
		}
	}
}

// Join: a device-bound token presented without / with another device id does not identify the visitor: the join
// degrades to a NEW visitor (usecase receives an empty VisitorID) instead of failing or reusing the old identity.
func TestDeviceBinding_JoinDegradesToNewVisitor(t *testing.T) {
	g := newDeviceRig(t)
	for _, tc := range []struct {
		name, device, wantVID string
	}{
		{"matching device keeps the visitor", rawDevice, "v1"},
		{"no device header -> new visitor", "", ""},
		{"other device -> new visitor", "another-device", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctrl := gomock.NewController(t)
			uc := mock_inbound.NewMockIJoinUsecase(ctrl)
			var seen string
			uc.EXPECT().Join(gomock.Any(), gomock.Any()).DoAndReturn(func(_ interface{ Done() <-chan struct{} }, in inbound.JoinInput) (dto.QueueEntry, error) {
				seen = in.VisitorID
				assert.NotContains(t, in.DeviceHash, rawDevice, "only the HMAC reaches the usecase")
				return dto.QueueEntry{EntryID: "e1"}, nil
			})
			mux := stdhttp.NewServeMux()
			queuehttp.NewJoinHandler(uc, false, 1).WithAuth(g.auth).Register(mux)
			req := httptest.NewRequest(stdhttp.MethodPost, "/api/v1/events/ev1/queue-entries", strings.NewReader("{}"))
			req.Header.Set("X-Visitor-Token", g.bound)
			if tc.device != "" {
				req.Header.Set("X-Device-Id", tc.device)
			}
			rec := httptest.NewRecorder()
			mux.ServeHTTP(rec, req)
			require.Equal(t, stdhttp.StatusCreated, rec.Code)
			assert.Equal(t, tc.wantVID, seen)
			assert.NotContains(t, rec.Body.String(), rawDevice)
		})
	}
}
