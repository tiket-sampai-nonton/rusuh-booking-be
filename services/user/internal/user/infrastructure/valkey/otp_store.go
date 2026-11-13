// Package valkey implements the OTP store and rate limiter on Valkey. Data di sini sementara (TTL):
// kehilangan data hanya berarti pengguna meminta kode baru (ADR-008 diperbarui, D-21).
// Operasi yang harus atomik (verifikasi OTP, mulai challenge, hitung kuota) memakai satu skrip Lua; angka bisnis
// tetap dari Go lewat ARGV (ADR-027).
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
	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/user/internal/user/port/outbound"
)

// Keyer applies the environment namespace (implemented by pkg/valkey.Client).
type Keyer interface {
	Key(k string) string
}

// verifyLua counts one attempt and compares in a single step. Hasil: {verdict, attempts}; verdict 0 NotFound,
// 1 Matched, 2 Mismatch, 3 Locked. Counter tidak pernah hidup lebih lama dari kode (PEXPIRE ke PTTL kode).
// Setelah terkunci, kode dihapus tetapi counter dibiarkan: kode yang tersisa tetap dijawab Locked sampai startLua
// memulai kode baru (yang menghapus counter). Counter hilang bersama kode, jadi tidak ada kunci yang lebih lama.
const verifyLua = `
local stored = redis.call('GET', KEYS[1])
if not stored then
  local n = tonumber(redis.call('GET', KEYS[2]) or '0')
  if n > tonumber(ARGV[2]) then return {3, n} end
  return {0, 0}
end
local n = redis.call('INCR', KEYS[2])
local ttl = redis.call('PTTL', KEYS[1])
if ttl > 0 then redis.call('PEXPIRE', KEYS[2], ttl) end
if n > tonumber(ARGV[2]) then
  redis.call('DEL', KEYS[1])
  return {3, n}
end
if stored == ARGV[1] then
  redis.call('DEL', KEYS[1], KEYS[2])
  return {1, n}
end
return {2, n}
`

// startLua sets the cooldown (NX), the code and resets the counter in one round trip; 0 = cooldown aktif.
const startLua = `
local ok = redis.call('SET', KEYS[3], '1', 'NX', 'PX', ARGV[3])
if not ok then return 0 end
redis.call('SET', KEYS[1], ARGV[1], 'PX', ARGV[2])
redis.call('DEL', KEYS[2])
return 1
`

// allowLua increments the window counter and (re)sets its expiry only on the first hit or when it lost its TTL.
const allowLua = `
local n = redis.call('INCR', KEYS[1])
if n == 1 or redis.call('PTTL', KEYS[1]) < 0 then redis.call('PEXPIRE', KEYS[1], ARGV[1]) end
return n
`

// releaseLua returns one unit if the bucket still exists and is positive; never creates a key, never goes below 0.
const releaseLua = `
local v = tonumber(redis.call('GET', KEYS[1]))
if v and v > 0 then redis.call('DECR', KEYS[1]) end
return 0
`

// OTPStore implements outbound.IOTPStore.
type OTPStore struct {
	c      vk.Client
	keyer  Keyer
	verify *vk.Lua
	start  *vk.Lua
}

// NewOTPStore returns the store. TTL datang dari pemanggil Start (satu sumber kebenaran).
func NewOTPStore(c vk.Client, keyer Keyer) *OTPStore {
	return &OTPStore{c: c, keyer: keyer, verify: vk.NewLuaScript(verifyLua), start: vk.NewLuaScript(startLua)}
}

// Semua key satu email memakai hash tag {key} agar berada di slot cluster yang sama.
func (s *OTPStore) keys(key string) (code, attempts, cooldown string) {
	base := s.keyer.Key("otp:{" + key + "}")
	return base, base + ":n", base + ":cd"
}

// InCooldown reports whether the resend cooldown key exists.
func (s *OTPStore) InCooldown(ctx context.Context, key string) (bool, error) {
	_, _, cd := s.keys(key)
	n, err := s.c.Do(ctx, s.c.B().Exists().Key(cd).Build()).AsInt64()
	if err != nil {
		return false, fmt.Errorf("valkey: otp cooldown: %w", err)
	}
	return n > 0, nil
}

// Start stores a new challenge unless the cooldown key still exists, atomically.
func (s *OTPStore) Start(ctx context.Context, key string, codeHash []byte, ttl, cooldown time.Duration) (bool, error) {
	if ttl < time.Millisecond || cooldown < time.Millisecond {
		return false, errors.New("valkey: otp start: ttl dan cooldown harus ≥ 1ms")
	}
	codeKey, attemptsKey, cdKey := s.keys(key)
	n, err := s.start.Exec(ctx, s.c, []string{codeKey, attemptsKey, cdKey},
		[]string{hex.EncodeToString(codeHash), strconv.FormatInt(ttl.Milliseconds(), 10), strconv.FormatInt(cooldown.Milliseconds(), 10)}).AsInt64()
	if err != nil {
		return false, fmt.Errorf("valkey: otp start: %w", err)
	}
	return n == 1, nil
}

// Verify counts one attempt and compares the candidate in a single script.
func (s *OTPStore) Verify(ctx context.Context, key string, candidateHash []byte, maxAttempts int) (entity.OTPAttempt, error) {
	codeKey, attemptsKey, _ := s.keys(key)
	arr, err := s.verify.Exec(ctx, s.c, []string{codeKey, attemptsKey},
		[]string{hex.EncodeToString(candidateHash), strconv.Itoa(maxAttempts)}).ToArray()
	if err != nil {
		return entity.OTPAttempt{}, fmt.Errorf("valkey: otp verify: %w", err)
	}
	if len(arr) != 2 {
		return entity.OTPAttempt{}, errors.New("valkey: otp verify: balasan tak terduga")
	}
	verdict, err := arr[0].AsInt64()
	if err != nil {
		return entity.OTPAttempt{}, fmt.Errorf("valkey: otp verify: %w", err)
	}
	attempts, err := arr[1].AsInt64()
	if err != nil {
		return entity.OTPAttempt{}, fmt.Errorf("valkey: otp verify: %w", err)
	}
	var v entity.OTPVerdict
	switch verdict {
	case 0:
		v = entity.OTPNotFound
	case 1:
		v = entity.OTPMatched
	case 2:
		v = entity.OTPMismatch
	case 3:
		v = entity.OTPLocked
	default:
		return entity.OTPAttempt{}, errors.New("valkey: otp verify: verdict tak dikenal")
	}
	return entity.OTPAttempt{Verdict: v, Attempts: int(attempts)}, nil
}

// Cancel deletes challenge, counter and cooldown (kode tidak terkirim; pengguna boleh langsung meminta lagi).
func (s *OTPStore) Cancel(ctx context.Context, key string) error {
	codeKey, attemptsKey, cdKey := s.keys(key)
	return s.c.Do(ctx, s.c.B().Del().Key(codeKey, attemptsKey, cdKey).Build()).Error()
}

// RateLimiter implements outbound.IRateLimiter with a fixed window.
type RateLimiter struct {
	c       vk.Client
	keyer   Keyer
	now     func() time.Time
	allow   *vk.Lua
	release *vk.Lua
}

// NewRateLimiter returns the limiter.
func NewRateLimiter(c vk.Client, keyer Keyer, now func() time.Time) *RateLimiter {
	return &RateLimiter{c: c, keyer: keyer, now: now, allow: vk.NewLuaScript(allowLua), release: vk.NewLuaScript(releaseLua)}
}

// Allow increments the counter of the current window and reports whether it is within limit.
// Ticket.Bucket adalah key lengkap yang dinaikkan, supaya Release tidak pernah mengenai jendela lain.
func (r *RateLimiter) Allow(ctx context.Context, key string, limit int, window time.Duration) (outbound.RateTicket, error) {
	if limit < 1 {
		return outbound.RateTicket{}, errors.New("valkey: rate limit: limit harus ≥ 1")
	}
	if window < time.Millisecond {
		return outbound.RateTicket{}, errors.New("valkey: rate limit: window harus ≥ 1ms")
	}
	bucket := r.now().UnixMilli() / window.Milliseconds()
	k := r.keyer.Key("rl:{" + key + "}:" + strconv.FormatInt(bucket, 10))
	n, err := r.allow.Exec(ctx, r.c, []string{k}, []string{strconv.FormatInt(window.Milliseconds(), 10)}).AsInt64()
	if err != nil {
		return outbound.RateTicket{}, fmt.Errorf("valkey: rate limit: %w", err)
	}
	return outbound.RateTicket{Allowed: n <= int64(limit), Bucket: k}, nil
}

// Release gives back one unit of the ticket's bucket (best effort; no-op bila bucket hilang atau 0).
func (r *RateLimiter) Release(ctx context.Context, t outbound.RateTicket) error {
	if t.Bucket == "" {
		return nil
	}
	if err := r.release.Exec(ctx, r.c, []string{t.Bucket}, nil).Error(); err != nil {
		return fmt.Errorf("valkey: rate release: %w", err)
	}
	return nil
}
