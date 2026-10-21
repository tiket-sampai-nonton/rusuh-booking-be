package usecase

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/tiket-sampai-nonton/rusuh-booking-be/pkg/clock"
	"github.com/tiket-sampai-nonton/rusuh-booking-be/pkg/rabbitmq"

	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/notification/internal/notification/domain/dto"
	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/notification/internal/notification/domain/entity"
	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/notification/internal/notification/domain/mapper"
	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/notification/internal/notification/port/outbound"
)

// NotifyTurnArrived implements inbound.INotifyTurnArrived: check the claim deadline, look up the
// subscription, and push "turn arrived" with the deadline in the payload.
type NotifyTurnArrived struct {
	repo  outbound.IDeliveryRepository
	subs  outbound.ISubscriptionRepository
	push  outbound.IPushSender
	pii   outbound.IPIIProtector
	clock clock.IClock
	log   *slog.Logger
}

// NewNotifyTurnArrived accepts interfaces and returns the concrete usecase.
func NewNotifyTurnArrived(repo outbound.IDeliveryRepository, subs outbound.ISubscriptionRepository, push outbound.IPushSender, pii outbound.IPIIProtector, clk clock.IClock, log *slog.Logger) *NotifyTurnArrived {
	return &NotifyTurnArrived{repo: repo, subs: subs, push: push, pii: pii, clock: clk, log: log}
}

// NotifyTurnArrived processes one queue.turn.arrived event.
func (u *NotifyTurnArrived) NotifyTurnArrived(ctx context.Context, sourceMessageID string, p dto.TurnArrivedPayload) error {
	if err := p.Validate(); err != nil {
		return rabbitmq.Permanent(err)
	}
	deadline, derr := p.ParseClaimDeadline()
	if derr != nil {
		return rabbitmq.Permanent(fmt.Errorf("parse claimDeadline: %w", derr))
	}

	if u.clock.Now().After(deadline) {
		// Late push is useless: claim+expire the row and discard (R5).
		if _, err := u.repo.ClaimDelivery(ctx, mapper.NewPushDelivery(sourceMessageID, entity.TypeTurnArrived, p.QueueEntryID, u.pii.HMACRecipient(p.VisitorID))); err != nil {
			return fmt.Errorf("claim delivery: %w", err)
		}
		_ = u.repo.MarkExpired(ctx, sourceMessageID, entity.TypeTurnArrived)
		u.log.Info("turn.arrived deadline lewat, buang", "messageId", sourceMessageID)
		return nil
	}

	sub, err := u.subs.FindByVisitorAndEvent(ctx, p.VisitorID, p.EventID)
	if errors.Is(err, entity.ErrNotFound) {
		u.log.Info("turn.arrived tanpa subscription, skip", "messageId", sourceMessageID)
		return nil
	}
	if err != nil {
		return fmt.Errorf("find subscription: %w", err)
	}

	token, terr := u.pii.UnsealPushToken(sub.PushTokenEnc)
	if terr != nil {
		return rabbitmq.Permanent(fmt.Errorf("decrypt push token: %w", terr))
	}

	claim := mapper.NewPushDelivery(sourceMessageID, entity.TypeTurnArrived, p.QueueEntryID, u.pii.HMACRecipient(p.VisitorID))
	claimed, err := u.repo.ClaimDelivery(ctx, claim)
	if err != nil {
		return fmt.Errorf("claim delivery: %w", err)
	}
	if !claimed {
		return nil
	}

	mins := int(time.Until(deadline).Minutes()) + 1
	msg := outbound.PushMessage{
		Token: token,
		Title: "Giliran Anda Tiba!",
		Body:  fmt.Sprintf("Segera claim dalam %d menit", mins),
		Data: map[string]string{
			"eventId": p.EventID, "queueEntryId": p.QueueEntryID,
			"type": "TURN_ARRIVED", "claimDeadline": deadline.UTC().Format(time.RFC3339),
		},
	}
	providerID, serr := u.push.Send(ctx, msg)
	if serr != nil {
		if u.push.IsTokenInvalid(serr) {
			_ = u.subs.Delete(ctx, p.VisitorID, p.EventID)
			_ = u.repo.MarkDead(ctx, sourceMessageID, entity.TypeTurnArrived, serr.Error())
			u.log.Info("turn.arrived token invalid, hapus subscription", "messageId", sourceMessageID)
			return nil
		}
		_ = u.repo.BumpAttempt(ctx, sourceMessageID, entity.TypeTurnArrived, serr.Error(), u.clock.Now().Add(retryDelay))
		return fmt.Errorf("send turn.arrived push: %w", serr)
	}
	return u.repo.MarkSent(ctx, sourceMessageID, entity.TypeTurnArrived, providerID)
}
