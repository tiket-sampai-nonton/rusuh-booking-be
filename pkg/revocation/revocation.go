// Package revocation lets a buyer access token stop working right after logout, before its exp. User Service writes the
// token's session id (claim "sid") to a Valkey denylist at logout; every service that accepts buyer tokens asks the same
// denylist. Entries live only as long as the longest access token, so Valkey never holds expired tokens.
package revocation

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/valkey-io/valkey-go"

	"github.com/tiket-sampai-nonton/rusuh-booking-be/pkg/token"
)

// ErrRevoked is returned for a token whose session was logged out.
var ErrRevoked = errors.New("revocation: sesi sudah logout")

// Leeway is added to the access token lifetime so clock skew between services never lets a revoked token slip through.
const Leeway = 30 * time.Second

// lookupTimeout bounds one denylist call so a slow Valkey cannot stall request handling.
const lookupTimeout = 500 * time.Millisecond

// Denylist stores revoked session ids.
type Denylist interface {
	// Revoke marks the session revoked for ttl (the longest remaining life of its access tokens plus Leeway).
	Revoke(ctx context.Context, sid string, ttl time.Duration) error
	// IsRevoked reports whether the session was revoked.
	IsRevoked(ctx context.Context, sid string) (bool, error)
}

// Keyer namespaces keys (pkg/valkey.Client implements it).
type Keyer interface {
	Key(k string) string
}

// Store is the Valkey implementation of Denylist.
type Store struct {
	c valkey.Client
	k Keyer
}

// NewStore returns a denylist backed by Valkey; k adds the environment prefix to keys.
func NewStore(c valkey.Client, k Keyer) *Store { return &Store{c: c, k: k} }

func (s *Store) key(sid string) string { return s.k.Key("rbt:revoked:sid:" + sid) }

// Revoke implements Denylist.
func (s *Store) Revoke(ctx context.Context, sid string, ttl time.Duration) error {
	if sid == "" || ttl <= 0 {
		return nil
	}
	if err := s.c.Do(ctx, s.c.B().Set().Key(s.key(sid)).Value("1").Px(ttl).Build()).Error(); err != nil {
		return fmt.Errorf("revocation: tulis denylist: %w", err)
	}
	return nil
}

// IsRevoked implements Denylist.
func (s *Store) IsRevoked(ctx context.Context, sid string) (bool, error) {
	n, err := s.c.Do(ctx, s.c.B().Exists().Key(s.key(sid)).Build()).AsInt64()
	if err != nil {
		return false, fmt.Errorf("revocation: baca denylist: %w", err)
	}
	return n > 0, nil
}

// Check rejects a verified token whose session was revoked. It fails closed: when the denylist cannot be read the token
// is refused, because an admin token that was logged out must not pass just because Valkey is down. Tokens without a
// session id (issued before this feature) and a nil denylist (not configured) are accepted.
func Check(d Denylist, c token.Claims) error {
	if d == nil || c.SessionID == "" {
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), lookupTimeout)
	defer cancel()
	revoked, err := d.IsRevoked(ctx, c.SessionID)
	if err != nil {
		return err
	}
	if revoked {
		return ErrRevoked
	}
	return nil
}
