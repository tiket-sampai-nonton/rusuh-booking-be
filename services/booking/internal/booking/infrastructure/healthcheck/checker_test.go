package healthcheck

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

type fakePinger struct {
	name  string
	err   error
	delay time.Duration
}

func (f fakePinger) Name() string { return f.name }
func (f fakePinger) Ping(ctx context.Context) error {
	select {
	case <-time.After(f.delay):
		return f.err
	case <-ctx.Done():
		return ctx.Err()
	}
}

func TestCheckAll(t *testing.T) {
	c := New([]Pinger{
		fakePinger{name: "postgres"},
		fakePinger{name: "valkey", err: errors.New("refused")},
		fakePinger{name: "rabbitmq", delay: time.Second},
		fakePinger{name: "Bad Name"},
	}, 50*time.Millisecond)

	got := c.CheckAll(context.Background())
	assert.Len(t, got, 4)
	assert.True(t, got[0].Up)
	assert.Equal(t, "postgres", got[0].Name.String())
	assert.False(t, got[1].Up)
	assert.Equal(t, "refused", got[1].Reason)
	assert.False(t, got[2].Up, "ping yang melewati timeout dianggap down")
	assert.Contains(t, got[2].Reason, "deadline")
	assert.False(t, got[3].Up)
	assert.Equal(t, "invalid dependency name", got[3].Reason)
}
