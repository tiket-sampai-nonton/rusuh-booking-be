package smtp

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
)

// fakeSMTP is a minimal SMTP server that records one message.
type fakeSMTP struct {
	ln   net.Listener
	mu   sync.Mutex
	data string
	rcpt string
	fail string // perintah yang dijawab 550
}

func startFake(t *testing.T, fail string) *fakeSMTP {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	f := &fakeSMTP{ln: ln, fail: fail}
	go f.serve()
	t.Cleanup(func() { _ = ln.Close() })
	return f
}

func (f *fakeSMTP) serve() {
	conn, err := f.ln.Accept()
	if err != nil {
		return
	}
	defer func() { _ = conn.Close() }()
	r := bufio.NewReader(conn)
	write := func(s string) { _, _ = conn.Write([]byte(s + "\r\n")) }
	write("220 fake ESMTP")
	for {
		line, err := r.ReadString('\n')
		if err != nil {
			return
		}
		cmd := strings.ToUpper(strings.TrimSpace(line))
		if f.fail != "" && strings.HasPrefix(cmd, f.fail) {
			write("550 rejected")
			continue
		}
		switch {
		case strings.HasPrefix(cmd, "EHLO"), strings.HasPrefix(cmd, "HELO"):
			write("250 fake")
		case strings.HasPrefix(cmd, "MAIL FROM"):
			write("250 ok")
		case strings.HasPrefix(cmd, "RCPT TO"):
			f.mu.Lock()
			f.rcpt = strings.TrimSpace(line)
			f.mu.Unlock()
			write("250 ok")
		case cmd == "DATA":
			write("354 end with .")
			var b strings.Builder
			for {
				l, err := r.ReadString('\n')
				if err != nil || l == ".\r\n" {
					break
				}
				b.WriteString(l)
			}
			f.mu.Lock()
			f.data = b.String()
			f.mu.Unlock()
			write("250 queued")
		case cmd == "QUIT":
			write("221 bye")
			return
		default:
			write("250 ok")
		}
	}
}

func (f *fakeSMTP) port() int { return f.ln.Addr().(*net.TCPAddr).Port }

func newMailer(t *testing.T, port int) *Mailer {
	t.Helper()
	m, err := New(Config{Host: "127.0.0.1", Port: port, From: "Rusuh Booking Tiket <no-reply@rusuh.test>", Timeout: 2 * time.Second},
		func() time.Time { return time.Date(2026, 10, 19, 0, 0, 0, 0, time.UTC) })
	require.NoError(t, err)
	return m
}

func TestSendOTP(t *testing.T) {
	f := startFake(t, "")
	require.NoError(t, newMailer(t, f.port()).SendOTP(context.Background(), "arnu@gmail.com", "042517", 5*time.Minute))

	f.mu.Lock()
	defer f.mu.Unlock()
	assert.Contains(t, f.rcpt, "arnu@gmail.com")
	assert.Contains(t, f.data, "042517")
	assert.Contains(t, f.data, "berlaku 5 menit")
	assert.Contains(t, f.data, "To: arnu@gmail.com")
	assert.Contains(t, f.data, "no-reply@rusuh.test")
}

func TestSendOTP_Errors(t *testing.T) {
	assert.Error(t, newMailer(t, 1).SendOTP(context.Background(), "x@y.id", "1", time.Minute), "dial gagal")
	assert.Error(t, newMailer(t, 1).SendOTP(context.Background(), "bukan email", "1", time.Minute))

	f := startFake(t, "RCPT")
	err := newMailer(t, f.port()).SendOTP(context.Background(), "a@b.id", "123456", time.Minute)
	assert.ErrorContains(t, err, "RCPT TO")

	_, err = New(Config{From: "tanpa-alamat"}, time.Now)
	assert.Error(t, err)
}
