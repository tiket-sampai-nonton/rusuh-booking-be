// Package smtp sends the OTP email directly over SMTP. OTP adalah secret sehingga tidak dikirim lewat
// RabbitMQ/Notification Service (envelope event tidak boleh memuat secret, SDD §4.6.2).
package smtp

import (
	"context"
	"crypto/tls"
	"fmt"
	"mime"
	"net"
	"net/mail"
	gosmtp "net/smtp"
	"strconv"
	"strings"
	"time"
)

// Config is the SMTP connection.
type Config struct {
	Host     string
	Port     int
	Username string
	Password string
	From     string
	Timeout  time.Duration
}

// Mailer implements outbound.IMailer.
type Mailer struct {
	cfg  Config
	from *mail.Address
	now  func() time.Time
}

// New validates From and returns the mailer.
func New(cfg Config, now func() time.Time) (*Mailer, error) {
	from, err := mail.ParseAddress(cfg.From)
	if err != nil {
		return nil, fmt.Errorf("smtp: SMTP_FROM: %w", err)
	}
	return &Mailer{cfg: cfg, from: from, now: now}, nil
}

// SendOTP sends the code. Isi email tidak pernah dicatat di log.
func (m *Mailer) SendOTP(ctx context.Context, to, code string, ttl time.Duration) error {
	rcpt, err := mail.ParseAddress(to)
	if err != nil {
		return fmt.Errorf("smtp: alamat tujuan: %w", err)
	}
	body := fmt.Sprintf("Kode masuk Rusuh Booking Tiket kamu: %s\r\n\r\nKode berlaku %d menit dan hanya bisa dipakai sekali.\r\nJangan berikan kode ini kepada siapa pun, termasuk yang mengaku petugas.\r\nAbaikan email ini bila kamu tidak meminta kode.\r\n",
		code, int(ttl.Minutes()))
	return m.send(ctx, rcpt.Address, mime.QEncoding.Encode("utf-8", "Kode masuk Rusuh Booking Tiket"), body)
}

func (m *Mailer) send(ctx context.Context, to, subject, body string) error {
	addr := net.JoinHostPort(m.cfg.Host, strconv.Itoa(m.cfg.Port))
	d := net.Dialer{Timeout: m.cfg.Timeout}
	conn, err := d.DialContext(ctx, "tcp", addr)
	if err != nil {
		return fmt.Errorf("smtp: dial %s: %w", addr, err)
	}
	_ = conn.SetDeadline(time.Now().Add(m.cfg.Timeout)) // deadline memakai jam nyata, bukan jam yang diinjeksi
	c, err := gosmtp.NewClient(conn, m.cfg.Host)
	if err != nil {
		_ = conn.Close()
		return fmt.Errorf("smtp: handshake: %w", err)
	}
	defer func() { _ = c.Close() }()
	if ok, _ := c.Extension("STARTTLS"); ok {
		if err := c.StartTLS(&tls.Config{ServerName: m.cfg.Host, MinVersion: tls.VersionTLS12}); err != nil {
			return fmt.Errorf("smtp: starttls: %w", err)
		}
	}
	if m.cfg.Username != "" {
		if err := c.Auth(gosmtp.PlainAuth("", m.cfg.Username, m.cfg.Password, m.cfg.Host)); err != nil {
			return fmt.Errorf("smtp: auth: %w", err)
		}
	}
	if err := c.Mail(m.from.Address); err != nil {
		return fmt.Errorf("smtp: MAIL FROM: %w", err)
	}
	if err := c.Rcpt(to); err != nil {
		return fmt.Errorf("smtp: RCPT TO: %w", err)
	}
	w, err := c.Data()
	if err != nil {
		return fmt.Errorf("smtp: DATA: %w", err)
	}
	msg := strings.Join([]string{
		"From: " + m.from.String(),
		"To: " + to,
		"Subject: " + subject,
		"Date: " + m.now().Format(time.RFC1123Z),
		"MIME-Version: 1.0",
		"Content-Type: text/plain; charset=utf-8",
		"Content-Transfer-Encoding: 8bit",
		"Auto-Submitted: auto-generated",
		"", body,
	}, "\r\n")
	if _, err := w.Write([]byte(msg)); err != nil {
		return fmt.Errorf("smtp: tulis pesan: %w", err)
	}
	if err := w.Close(); err != nil {
		return fmt.Errorf("smtp: akhiri DATA: %w", err)
	}
	return c.Quit()
}
