package valkey

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// A window shorter than 1ms is a caller programming error: reject before any Valkey call (REQ-QSH-13.3).
func TestRateLimiter_WindowBelowOneMillisecondIsAnError(t *testing.T) {
	rl := NewRateLimiter(nil, nil, time.Now)
	_, err := rl.Allow(context.Background(), "k", 10, 0)
	require.Error(t, err)
	_, err = rl.Allow(context.Background(), "k", 10, 500*time.Microsecond)
	require.Error(t, err)
}
