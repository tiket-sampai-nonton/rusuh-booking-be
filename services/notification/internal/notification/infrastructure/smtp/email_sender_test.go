package smtp_test

import (
	"bufio"
	"context"
	"net"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/notification/internal/notification/infrastructure/smtp"
)

// fakeSMTP is a tiny SMTP server that accepts one message and records the DATA body.
type fakeSMTP struct {
	ln   net.Listener
	mu   sync.Mutex
	body string
}

func startFakeSMTP(t *testing.T) *fakeSMTP {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	f := &fakeSMTP{ln: ln}
	go f.accept()
	t.Cleanup(func() { _ = ln.Close() })
	return f
}

func (f *fakeSMTP) addr() (string, int) {
	a := f.ln.Addr().(*net.TCPAddr)
	return "127.0.0.1", a.Port
}

func (f *fakeSMTP) accept() {
	conn, err := f.ln.Accept()
	if err != nil {
		return
	}
	defer func() { _ = conn.Close() }()
	r := bufio.NewReader(conn)
	w := conn
	write := func(s string) { _, _ = w.Write([]byte(s + "\r\n")) }

	write("220 fake ESMTP")
	inData := false
	var sb strings.Builder
	for {
		_ = conn.SetReadDeadline(time.Now().Add(2 * time.Second))
		line, err := r.ReadString('\n')
		if err != nil {
			return
		}
		trimmed := strings.TrimRight(line, "\r\n")
		if inData {
			if trimmed == "." {
				inData = false
				f.mu.Lock()
				f.body = sb.String()
				f.mu.Unlock()
				write("250 OK queued")
				continue
			}
			sb.WriteString(trimmed + "\n")
			continue
		}
		switch {
		case strings.HasPrefix(trimmed, "EHLO"), strings.HasPrefix(trimmed, "HELO"):
			write("250-fake")
			write("250 OK")
		case strings.HasPrefix(trimmed, "MAIL"), strings.HasPrefix(trimmed, "RCPT"):
			write("250 OK")
		case strings.HasPrefix(trimmed, "DATA"):
			write("354 start mail input")
			inData = true
		case strings.HasPrefix(trimmed, "QUIT"):
			write("221 bye")
			return
		default:
			write("250 OK")
		}
	}
}

func TestSender_SendEmail(t *testing.T) {
	f := startFakeSMTP(t)
	host, port := f.addr()
	s := smtp.New(smtp.Config{Host: host, Port: port, From: "no-reply@rusuh.example", Timeout: 2 * time.Second})

	id, err := s.SendEmail(context.Background(), "buyer@example.id", "[RBT] Hi", "hello world")
	require.NoError(t, err)
	assert.NotEmpty(t, id)

	f.mu.Lock()
	body := f.body
	f.mu.Unlock()
	assert.Contains(t, body, "Subject: [RBT] Hi")
	assert.Contains(t, body, "To: buyer@example.id")
	assert.Contains(t, body, "hello world")
	assert.Contains(t, body, "Message-ID:")
}

func TestSender_DialErrorIsReturned(t *testing.T) {
	// Nothing listening on this port → transient dial error (retryable).
	s := smtp.New(smtp.Config{Host: "127.0.0.1", Port: 1, From: "x@y.id", Timeout: time.Second})
	_, err := s.SendEmail(context.Background(), "a@b.id", "s", "b")
	require.Error(t, err)
}
