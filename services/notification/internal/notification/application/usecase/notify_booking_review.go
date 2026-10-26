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

// NotifyBookingReview implements inbound.INotifyBookingReview: render the review email and send it.
type NotifyBookingReview struct {
	repo  outbound.IDeliveryRepository
	tmpl  outbound.ITemplateRenderer
	email outbound.IEmailSender
	pii   outbound.IPIIProtector
	clock clock.IClock
	log   *slog.Logger
}

// NewNotifyBookingReview accepts interfaces and returns the concrete usecase.
func NewNotifyBookingReview(repo outbound.IDeliveryRepository, tmpl outbound.ITemplateRenderer, email outbound.IEmailSender, pii outbound.IPIIProtector, clk clock.IClock, log *slog.Logger) *NotifyBookingReview {
	return &NotifyBookingReview{repo: repo, tmpl: tmpl, email: email, pii: pii, clock: clk, log: log}
}

// NotifyBookingReview processes one booking.review event (idempotent via ClaimDelivery).
func (u *NotifyBookingReview) NotifyBookingReview(ctx context.Context, sourceMessageID string, p dto.BookingReviewPayload) error {
	if err := p.Validate(); err != nil {
		return rabbitmq.Permanent(err)
	}

	claim := mapper.NewEmailDelivery(sourceMessageID, entity.TypeBookingReview, p.BookingID, u.pii.HMACRecipient(p.BuyerEmail))
	claimed, err := u.repo.ClaimDelivery(ctx, claim)
	if err != nil {
		return fmt.Errorf("claim delivery: %w", err)
	}
	if !claimed {
		u.log.Info("booking.review duplikat, skip", "messageId", sourceMessageID)
		return nil
	}

	body, html, rerr := renderBoth(u.tmpl, "review", dto.ReviewData{BookingID: p.BookingID, AmountMinor: p.AmountMinor, AttemptHint: p.AttemptHint})
	if rerr != nil {
		_ = u.repo.MarkDead(ctx, sourceMessageID, entity.TypeBookingReview, rerr.Error())
		return rabbitmq.Permanent(fmt.Errorf("render review: %w", rerr))
	}

	providerID, serr := u.email.SendEmail(ctx, p.BuyerEmail, "[RBT] Pembayaran Perlu Ditinjau", body, html, idempotencyKey(sourceMessageID, entity.TypeBookingReview))
	if serr != nil {
		recordSendFailure(ctx, u.repo, sourceMessageID, entity.TypeBookingReview, u.clock.Now(), serr)
		return fmt.Errorf("send review email: %w", serr)
	}
	return u.repo.MarkSent(ctx, sourceMessageID, entity.TypeBookingReview, providerID)
}
