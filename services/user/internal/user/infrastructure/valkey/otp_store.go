// Package valkey implements the OTP store and rate limiter on Valkey. Data di sini sementara (TTL):
// kehilangan data hanya berarti pengguna meminta kode baru (ADR-008 diperbarui, D-21).
package valkey

import (
	"context"
	"encoding/hex"
	"errors"
	"fmt"
	"strconv"
	"time"

	vk "github.com/valkey-io/valkey-go"

	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/user/internal/user/domain/entity"
)

// Keyer applies the environment namespace (implemented by pkg/valkey.Client).
type Keyer interface {
	Key(k string) string
}

// OTPStore implements outbound.IOTPStore.
type OTPStore struct {
	c     vk.Client
	keyer Keyer
	ttl   time.Duration
}

// NewOTPStore returns the store. ttl dipakai juga untuk counter percobaan.
func NewOTPStore(c vk.Client, keyer Keyer, ttl time.Duration) *OTPStore {
	return &OTPStore{c: c, keyer: keyer, ttl: ttl}
}

// Semua key satu email memakai hash tag {key} agar berada di slot cluster yang sama.
func (s *OTPStore) keys(key string) (code, attempts, cooldown string) {
	base := s.keyer.Key("otp:{" + key + "}")
	return base, base + ":n", base + ":cd"
}

// Start stores a new challenge unless the cooldown key still exists.
func (s *OTPStore) Start(ctx context.Context, key string, codeHash []byte, ttl, cooldown time.Duration) (bool, error) {
	codeKey, attemptsKey, cdKey := s.keys(key)
	err := s.c.Do(ctx, s.c.B().Set().Key(cdKey).Value("1").Nx().Px(cooldown).Build()).Error()
	if vk.IsValkeyNil(err) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("valkey: otp cooldown: %w", err)
	}
	for _, resp := range s.c.DoMulti(ctx,
		s.c.B().Set().Key(codeKey).Value(hex.EncodeToString(codeHash)).Px(ttl).Build(),
		s.c.B().Del().Key(attemptsKey).Build(),
	) {
		if err := resp.Error(); err != nil {
			return false, fmt.Errorf("valkey: otp start: %w", err)
		}
	}
	return true, nil
}

// Attempt increments the counter and returns the challenge.
func (s *OTPStore) Attempt(ctx context.Context, key string) (entity.OTPChallenge, bool, error) {
	codeKey, attemptsKey, _ := s.keys(key)
	hexHash, err := s.c.Do(ctx, s.c.B().Get().Key(codeKey).Build()).ToString()
	if vk.IsValkeyNil(err) {
		return entity.OTPChallenge{}, false, nil
	}
	if err != nil {
		return entity.OTPChallenge{}, false, fmt.Errorf("valkey: otp get: %w", err)
	}
	resps := s.c.DoMulti(ctx,
		s.c.B().Incr().Key(attemptsKey).Build(),
		s.c.B().Pexpire().Key(attemptsKey).Milliseconds(s.ttl.Milliseconds()).Build(),
	)
	n, err := resps[0].AsInt64()
	if err != nil {
		return entity.OTPChallenge{}, false, fmt.Errorf("valkey: otp incr: %w", err)
	}
	hash, err := hex.DecodeString(hexHash)
	if err != nil {
		return entity.OTPChallenge{}, false, errors.New("valkey: otp hash rusak")
	}
	return entity.OTPChallenge{CodeHash: hash, Attempts: int(n)}, true, nil
}

// Clear deletes the challenge and its counter (cooldown tetap berlaku).
func (s *OTPStore) Clear(ctx context.Context, key string) error {
	codeKey, attemptsKey, _ := s.keys(key)
	return s.c.Do(ctx, s.c.B().Del().Key(codeKey, attemptsKey).Build()).Error()
}

// RateLimiter implements outbound.IRateLimiter with a fixed window.
type RateLimiter struct {
	c     vk.Client
	keyer Keyer
	now   func() time.Time
}

// NewRateLimiter returns the limiter.
func NewRateLimiter(c vk.Client, keyer Keyer, now func() time.Time) *RateLimiter {
	return &RateLimiter{c: c, keyer: keyer, now: now}
}

// Allow increments the counter of the current window and reports whether it is within limit.
func (r *RateLimiter) Allow(ctx context.Context, key string, limit int, window time.Duration) (bool, error) {
	bucket := r.now().UnixMilli() / window.Milliseconds()
	k := r.keyer.Key("rl:{" + key + "}:" + strconv.FormatInt(bucket, 10))
	resps := r.c.DoMulti(ctx,
		r.c.B().Incr().Key(k).Build(),
		r.c.B().Pexpire().Key(k).Milliseconds(window.Milliseconds()).Build(),
	)
	n, err := resps[0].AsInt64()
	if err != nil {
		return false, fmt.Errorf("valkey: rate limit: %w", err)
	}
	return n <= int64(limit), nil
}
