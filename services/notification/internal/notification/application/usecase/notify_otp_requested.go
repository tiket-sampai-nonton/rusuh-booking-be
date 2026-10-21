package usecase

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/tiket-sampai-nonton/rusuh-booking-be/pkg/clock"
	"github.com/tiket-sampai-nonton/rusuh-booking-be/pkg/rabbitmq"

	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/notification/internal/notification/domain/dto"
	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/notification/internal/notification/port/outbound"
)

// NotifyOTPRequested implements inbound.INotifyOTPRequested. OTP is time-sensitive: it is NOT
// tracked in the deliveries table (schema has no OTP type) and SMTP failures are NOT retried —
// the event is acked and the user can request a new code.
type NotifyOTPRequested struct {
	tmpl  outbound.ITemplateRenderer
	email outbound.IEmailSender
	pii   outbound.IPIIProtector
	clock clock.IClock
	log   *slog.Logger
}

// NewNotifyOTPRequested accepts interfaces and returns the concrete usecase.
func NewNotifyOTPRequested(tmpl outbound.ITemplateRenderer, email outbound.IEmailSender, pii outbound.IPIIProtector, clk clock.IClock, log *slog.Logger) *NotifyOTPRequested {
	return &NotifyOTPRequested{tmpl: tmpl, email: email, pii: pii, clock: clk, log: log}
}

// NotifyOTPRequested processes one otp.requested event.
func (u *NotifyOTPRequested) NotifyOTPRequested(ctx context.Context, sourceMessageID string, p dto.OTPRequestedPayload) error {
	if err := p.Validate(); err != nil {
		return rabbitmq.Permanent(err)
	}
	expiresAt, perr := p.ParseExpiresAt()
	if perr != nil {
		return rabbitmq.Permanent(fmt.Errorf("parse expiresAt: %w", perr))
	}
	if u.clock.Now().After(expiresAt) {
		u.log.Warn("OTP expired, skip", "messageId", sourceMessageID)
		return nil // expired → ack, discard
	}

	emailEnc, derr := p.DecodeEmailEnc()
	if derr != nil {
		return rabbitmq.Permanent(fmt.Errorf("decode emailEnc: %w", derr))
	}
	codeEnc, derr := p.DecodeCodeEnc()
	if derr != nil {
		return rabbitmq.Permanent(fmt.Errorf("decode codeEnc: %w", derr))
	}

	email, uerr := u.pii.UnsealEmail(emailEnc)
	if uerr != nil {
		return rabbitmq.Permanent(fmt.Errorf("decrypt email: %w", uerr)) // TC-MSG-06: decrypt error → DLQ
	}
	code, uerr := u.pii.UnsealOTP(codeEnc)
	if uerr != nil {
		return rabbitmq.Permanent(fmt.Errorf("decrypt code: %w", uerr))
	}

	body, rerr := u.tmpl.Render("otp_login", dto.OTPData{Code: code, ExpiresAt: expiresAt, Locale: p.LocaleOrDefault()})
	if rerr != nil {
		return rabbitmq.Permanent(fmt.Errorf("render otp_login: %w", rerr))
	}

	// No retry: SMTP failure is logged and acked (time-sensitive, TTL ~5m).
	if _, serr := u.email.SendEmail(ctx, email, "Kode Login Anda", body); serr != nil {
		u.log.Error("OTP email gagal (tanpa retry)", "messageId", sourceMessageID, "error", serr)
		return nil
	}
	u.log.Info("OTP email terkirim", "messageId", sourceMessageID, "validUntil", expiresAt.Format(time.RFC3339))
	return nil
}
