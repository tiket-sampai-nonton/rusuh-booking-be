package httpx

import (
	"net/http"
	"sync"
	"time"

	"github.com/tiket-sampai-nonton/rusuh-booking-be/pkg/token"
)

const (
	serviceTokenTTL     = ServiceTokenMaxTTL
	serviceTokenRefresh = 30 * time.Second // re-sign when less than this remains
)

// ServiceTokenSource issues the bearer token this service sends to other services' /internal/v1/*.
// The token is cached and signed again only when less than 30 seconds of its 5 minute life remain,
// so callers do not sign on every request (R2.4). Safe for concurrent use.
type ServiceTokenSource struct {
	signer   *token.Signer
	issuer   string // this service, e.g. "rbt-payment"
	audience string // target service, e.g. "rbt-booking"
	now      func() time.Time

	mu      sync.Mutex
	cached  string
	expires time.Time
}

// NewServiceTokenSource returns a source that signs with signer as issuer for audience.
func NewServiceTokenSource(signer *token.Signer, issuer, audience string, now func() time.Time) *ServiceTokenSource {
	return &ServiceTokenSource{signer: signer, issuer: issuer, audience: audience, now: now}
}

// Token returns a valid service token, signing a new one when needed.
func (s *ServiceTokenSource) Token() (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := s.now()
	if s.cached != "" && s.expires.Sub(now) > serviceTokenRefresh {
		return s.cached, nil
	}
	tok, err := s.signer.Sign(token.Claims{
		Issuer: s.issuer, Subject: s.issuer, Audience: s.audience, ID: newID(),
		IssuedAt: now.Unix(), ExpiresAt: now.Add(serviceTokenTTL).Unix(),
	})
	if err != nil {
		return "", err
	}
	s.cached, s.expires = tok, now.Add(serviceTokenTTL)
	return tok, nil
}

// Authorize adds the bearer token, X-Request-ID, and trace headers to an outgoing internal request.
func (s *ServiceTokenSource) Authorize(req *http.Request) error {
	tok, err := s.Token()
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+tok)
	Propagate(req.Context(), req)
	return nil
}
