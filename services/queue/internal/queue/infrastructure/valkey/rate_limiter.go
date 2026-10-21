package valkey

import (
	"context"
	"fmt"
	"strconv"
	"time"

	vk "github.com/valkey-io/valkey-go"
)

// RateLimiter implements outbound.IRateLimiter with a shared fixed-window counter (design §3.1).
// Same mechanism as the User service limiter.
type RateLimiter struct {
	c     vk.Client
	keyer Keyer
	now   func() time.Time
}

// NewRateLimiter returns the limiter.
func NewRateLimiter(c vk.Client, keyer Keyer, now func() time.Time) *RateLimiter {
	return &RateLimiter{c: c, keyer: keyer, now: now}
}

// Allow increments the current window's counter and reports whether it is within limit.
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
