package valkey

import (
	"context"
	"fmt"
	"strconv"
	"time"

	vk "github.com/valkey-io/valkey-go"

	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/queue/assets/lua"
)

// RateLimiter implements outbound.IRateLimiter with a shared fixed-window counter (design §3.1, §5.7).
// INCR and PEXPIRE run in one Lua script so no counter can be left without a TTL.
type RateLimiter struct {
	c      vk.Client
	keyer  Keyer
	now    func() time.Time
	script *vk.Lua
}

// NewRateLimiter returns the limiter.
func NewRateLimiter(c vk.Client, keyer Keyer, now func() time.Time) *RateLimiter {
	return &RateLimiter{c: c, keyer: keyer, now: now, script: vk.NewLuaScript(lua.RateLimit)}
}

// Allow increments the current window's counter and reports whether it is within limit. A Valkey error is returned
// (fail-closed, QSH-OQ-5); a window shorter than 1ms is a caller bug and is rejected before any call.
func (r *RateLimiter) Allow(ctx context.Context, key string, limit int, window time.Duration) (bool, error) {
	if window < time.Millisecond {
		return false, fmt.Errorf("valkey: rate limit: window %s kurang dari 1ms", window)
	}
	bucket := r.now().UnixMilli() / window.Milliseconds()
	k := r.keyer.Key("rl:{" + key + "}:" + strconv.FormatInt(bucket, 10))
	n, err := r.script.Exec(ctx, r.c, []string{k}, []string{strconv.FormatInt(window.Milliseconds(), 10)}).AsInt64()
	if err != nil {
		return false, fmt.Errorf("valkey: rate limit: %w", err)
	}
	return n <= int64(limit), nil
}
