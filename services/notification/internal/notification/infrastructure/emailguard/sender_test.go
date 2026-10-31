package emailguard_test

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tiket-sampai-nonton/rusuh-booking-be/pkg/rabbitmq"
	"github.com/tiket-sampai-nonton/rusuh-booking-be/pkg/resilience"

	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/notification/internal/notification/infrastructure/emailguard"
	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/notification/internal/notification/port/outbound"
)

// fakeSender records calls and returns the scripted error.
type fakeSender struct {
	mu    sync.Mutex
	calls int
	err   error
	block chan struct{}
}

func (f *fakeSender) SendEmail(ctx context.Context, _, _, _, _, _ string, _ ...outbound.InlineImage) (string, error) {
	f.mu.Lock()
	f.calls++
	err, block := f.err, f.block
	f.mu.Unlock()
	if block != nil {
		<-block
	}
	if err != nil {
		return "", err
	}
	return "msg-1", nil
}

func (f *fakeSender) callCount() int { f.mu.Lock(); defer f.mu.Unlock(); return f.calls }

type clock struct{ t time.Time }

func (c *clock) now() time.Time { return c.t }

func send(s *emailguard.Sender) (string, error) {
	return s.SendEmail(context.Background(), "a@b.id", "subj", "body", "", "key")
}

func TestSender_PassesThroughSuccess(t *testing.T) {
	s, err := emailguard.New(&fakeSender{}, time.Now)
	require.NoError(t, err)
	id, err := send(s)
	require.NoError(t, err)
	assert.Equal(t, "msg-1", id)
}

// Property 5: provider down → fast transient failure, never a permanent error that loses the email.
func TestSender_OpensAfterConsecutiveFailuresAndRecovers(t *testing.T) {
	fake := &fakeSender{err: errors.New("smtp down")}
	clk := &clock{t: time.Now()}
	s, err := emailguard.New(fake, clk.now)
	require.NoError(t, err)

	for i := 0; i < 5; i++ {
		_, err = send(s)
		require.Error(t, err)
	}
	callsBeforeOpen := fake.callCount()

	_, err = send(s)
	require.ErrorIs(t, err, resilience.ErrOpen)
	assert.False(t, rabbitmq.IsPermanent(err), "breaker OPEN adalah kegagalan transien → retry")
	assert.Equal(t, callsBeforeOpen, fake.callCount(), "provider tidak dipanggil saat OPEN")

	fake.mu.Lock()
	fake.err = nil
	fake.mu.Unlock()
	clk.t = clk.t.Add(31 * time.Second)
	id, err := send(s)
	require.NoError(t, err, "probe half-open berhasil menutup breaker")
	assert.Equal(t, "msg-1", id)
}

func TestSender_PermanentErrorsDoNotOpenBreaker(t *testing.T) {
	perm := rabbitmq.Permanent(errors.New("alamat ditolak"))
	fake := &fakeSender{err: perm}
	s, err := emailguard.New(fake, time.Now)
	require.NoError(t, err)

	for i := 0; i < 10; i++ {
		_, err = send(s)
		require.Error(t, err)
		assert.True(t, rabbitmq.IsPermanent(err), "error permanen tetap permanen (→ DLQ)")
	}
	assert.Equal(t, 10, fake.callCount(), "10 penolakan bisnis tidak membuka breaker")
}

func TestSender_BulkheadFullIsTransientAndDoesNotOpenBreaker(t *testing.T) {
	block := make(chan struct{})
	fake := &fakeSender{block: block}
	s, err := emailguard.New(fake, time.Now)
	require.NoError(t, err)

	var wg sync.WaitGroup
	for i := 0; i < 10; i++ { // memenuhi 10 slot
		wg.Add(1)
		go func() { defer wg.Done(); _, _ = send(s) }()
	}
	require.Eventually(t, func() bool { return fake.callCount() == 10 }, 2*time.Second, 5*time.Millisecond)

	for i := 0; i < 8; i++ { // lebih dari ambang breaker (5)
		_, err = send(s)
		require.ErrorIs(t, err, resilience.ErrBulkheadFull)
		assert.False(t, rabbitmq.IsPermanent(err))
	}
	close(block)
	wg.Wait()

	_, err = send(s)
	require.NoError(t, err, "bulkhead penuh tidak membuka breaker")
}
