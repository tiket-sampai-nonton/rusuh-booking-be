// Package smtp implements IEmailSender with net/smtp (STARTTLS + PLAIN auth), targeting SES in
// production and Mailpit locally.
package smtp

import (
	"context"
	"crypto/tls"
	"fmt"
	"net"
	"net/smtp"
	"strconv"
	"strings"
	"time"

	"github.com/tiket-sampai-nonton/rusuh-booking-be/pkg/rabbitmq"
)

// Config holds the SMTP_* settings.
type Config struct {
	Host     string
	Port     int
	User     string
	Password string
	From     string
	Timeout  time.Duration
}

// Sender implements outbound.IEmailSender.
type Sender struct {
	cfg Config
}

// New returns the SMTP sender. The connection is opened per send (short-lived like SES).
func New(cfg Config) *Sender {
	if cfg.Timeout <= 0 {
		cfg.Timeout = 10 * time.Second
	}
	return &Sender{cfg: cfg}
}

// SendEmail builds an RFC822 text/plain message and delivers it, returning the Message-ID header.
func (s *Sender) SendEmail(ctx context.Context, to, subject, body string) (string, error) {
	messageID := fmt.Sprintf("<%s@notification.rusuh>", rabbitmq.NewID())
	msg := s.build(messageID, to, subject, body)

	done := make(chan error, 1)
	go func() { done <- s.deliver(to, msg) }()

	select {
	case <-ctx.Done():
		return "", ctx.Err()
	case err := <-done:
		if err != nil {
			return "", err
		}
		return messageID, nil
	}
}

func (s *Sender) build(messageID, to, subject, body string) []byte {
	var b strings.Builder
	b.WriteString("From: " + s.cfg.From + "\r\n")
	b.WriteString("To: " + to + "\r\n")
	b.WriteString("Subject: " + subject + "\r\n")
	b.WriteString("Message-ID: " + messageID + "\r\n")
	b.WriteString("Date: " + time.Now().UTC().Format(time.RFC1123Z) + "\r\n")
	b.WriteString("MIME-Version: 1.0\r\n")
	b.WriteString("Content-Type: text/plain; charset=utf-8\r\n")
	b.WriteString("\r\n")
	b.WriteString(body)
	return []byte(b.String())
}

// deliver opens a connection, upgrades to STARTTLS when offered, authenticates (when credentials are
// set), and sends the message. A connection/auth failure is transient (retryable); the caller decides.
func (s *Sender) deliver(to string, msg []byte) error {
	addr := net.JoinHostPort(s.cfg.Host, strconv.Itoa(s.cfg.Port))
	conn, err := net.DialTimeout("tcp", addr, s.cfg.Timeout)
	if err != nil {
		return fmt.Errorf("smtp: dial %s: %w", addr, err)
	}
	c, err := smtp.NewClient(conn, s.cfg.Host)
	if err != nil {
		_ = conn.Close()
		return fmt.Errorf("smtp: client: %w", err)
	}
	defer func() { _ = c.Close() }()

	if ok, _ := c.Extension("STARTTLS"); ok {
		if err := c.StartTLS(&tls.Config{ServerName: s.cfg.Host, MinVersion: tls.VersionTLS12}); err != nil {
			return fmt.Errorf("smtp: starttls: %w", err)
		}
	}
	if s.cfg.User != "" {
		auth := smtp.PlainAuth("", s.cfg.User, s.cfg.Password, s.cfg.Host)
		if ok, _ := c.Extension("AUTH"); ok {
			if err := c.Auth(auth); err != nil {
				return fmt.Errorf("smtp: auth: %w", err)
			}
		}
	}
	if err := c.Mail(s.cfg.From); err != nil {
		return fmt.Errorf("smtp: mail from: %w", err)
	}
	if err := c.Rcpt(to); err != nil {
		return fmt.Errorf("smtp: rcpt: %w", err)
	}
	w, err := c.Data()
	if err != nil {
		return fmt.Errorf("smtp: data: %w", err)
	}
	if _, err := w.Write(msg); err != nil {
		_ = w.Close()
		return fmt.Errorf("smtp: write: %w", err)
	}
	if err := w.Close(); err != nil {
		return fmt.Errorf("smtp: close data: %w", err)
	}
	return c.Quit()
}
