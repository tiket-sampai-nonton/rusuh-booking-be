package usecase

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/tiket-sampai-nonton/rusuh-booking-be/pkg/clock"
	"github.com/tiket-sampai-nonton/rusuh-booking-be/pkg/rabbitmq"

	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/notification/internal/notification/domain/dto"
	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/notification/internal/notification/domain/entity"
	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/notification/internal/notification/domain/mapper"
	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/notification/internal/notification/port/outbound"
)

// NotifyBookingExpired implements inbound.INotifyBookingExpired: render the expiry email and send it.
type NotifyBookingExpired struct {
	repo  outbound.IDeliveryRepository
	tmpl  outbound.ITemplateRenderer
	email outbound.IEmailSender
	pii   outbound.IPIIProtector
	clock clock.IClock
	log   *slog.Logger
}

// NewNotifyBookingExpired accepts interfaces and returns the concrete usecase.
func NewNotifyBookingExpired(repo outbound.IDeliveryRepository, tmpl outbound.ITemplateRenderer, email outbound.IEmailSender, pii outbound.IPIIProtector, clk clock.IClock, log *slog.Logger) *NotifyBookingExpired {
	return &NotifyBookingExpired{repo: repo, tmpl: tmpl, email: email, pii: pii, clock: clk, log: log}
}

// NotifyBookingExpired processes one booking.expired event (idempotent via ClaimDelivery).
func (u *NotifyBookingExpired) NotifyBookingExpired(ctx context.Context, sourceMessageID string, p dto.BookingExpiredPayload) error {
	if err := p.Validate(); err != nil {
		return rabbitmq.Permanent(err)
	}

	claim := mapper.NewEmailDelivery(sourceMessageID, entity.TypeBookingExpired, p.BookingID, u.pii.HMACRecipient(p.BuyerEmail))
	claimed, err := u.repo.ClaimDelivery(ctx, claim)
	if err != nil {
		return fmt.Errorf("claim delivery: %w", err)
	}
	if !claimed {
		u.log.Info("booking.expired duplikat, skip", "messageId", sourceMessageID)
		return nil
	}

	body, rerr := u.tmpl.Render("expired", dto.ExpiredData{PublicRef: p.PublicRef, Reason: p.Reason})
	if rerr != nil {
		_ = u.repo.MarkDead(ctx, sourceMessageID, entity.TypeBookingExpired, rerr.Error())
		return rabbitmq.Permanent(fmt.Errorf("render expired: %w", rerr))
	}

	subject := fmt.Sprintf("[RBT] Booking Kedaluwarsa — %s", p.PublicRef)
	providerID, serr := u.email.SendEmail(ctx, p.BuyerEmail, subject, body, "", idempotencyKey(sourceMessageID, entity.TypeBookingExpired))
	if serr != nil {
		recordSendFailure(ctx, u.repo, sourceMessageID, entity.TypeBookingExpired, u.clock.Now(), serr)
		return fmt.Errorf("send expired email: %w", serr)
	}
	return u.repo.MarkSent(ctx, sourceMessageID, entity.TypeBookingExpired, providerID)
}
