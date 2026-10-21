package usecase

import (
	"context"
	"errors"
	"fmt"
	"log/slog"

	"github.com/tiket-sampai-nonton/rusuh-booking-be/pkg/clock"
	"github.com/tiket-sampai-nonton/rusuh-booking-be/pkg/rabbitmq"

	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/notification/internal/notification/domain/dto"
	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/notification/internal/notification/domain/entity"
	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/notification/internal/notification/domain/mapper"
	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/notification/internal/notification/port/outbound"
)

// NotifyTurnSoon implements inbound.INotifyTurnSoon: look up the subscription and push "turn soon".
type NotifyTurnSoon struct {
	repo  outbound.IDeliveryRepository
	subs  outbound.ISubscriptionRepository
	push  outbound.IPushSender
	pii   outbound.IPIIProtector
	clock clock.IClock
	log   *slog.Logger
}

// NewNotifyTurnSoon accepts interfaces and returns the concrete usecase.
func NewNotifyTurnSoon(repo outbound.IDeliveryRepository, subs outbound.ISubscriptionRepository, push outbound.IPushSender, pii outbound.IPIIProtector, clk clock.IClock, log *slog.Logger) *NotifyTurnSoon {
	return &NotifyTurnSoon{repo: repo, subs: subs, push: push, pii: pii, clock: clk, log: log}
}

// NotifyTurnSoon processes one queue.turn.soon event.
func (u *NotifyTurnSoon) NotifyTurnSoon(ctx context.Context, sourceMessageID string, p dto.TurnSoonPayload) error {
	if err := p.Validate(); err != nil {
		return rabbitmq.Permanent(err)
	}

	sub, err := u.subs.FindByVisitorAndEvent(ctx, p.VisitorID, p.EventID)
	if errors.Is(err, entity.ErrNotFound) {
		u.log.Info("turn.soon tanpa subscription, skip", "messageId", sourceMessageID)
		return nil // R4: no subscription → ack
	}
	if err != nil {
		return fmt.Errorf("find subscription: %w", err)
	}

	token, derr := u.pii.UnsealPushToken(sub.PushTokenEnc)
	if derr != nil {
		return rabbitmq.Permanent(fmt.Errorf("decrypt push token: %w", derr))
	}

	claim := mapper.NewPushDelivery(sourceMessageID, entity.TypeTurnSoon, p.QueueEntryID, u.pii.HMACRecipient(p.VisitorID))
	claimed, err := u.repo.ClaimDelivery(ctx, claim)
	if err != nil {
		return fmt.Errorf("claim delivery: %w", err)
	}
	if !claimed {
		return nil // duplicate → ack
	}

	msg := outbound.PushMessage{
		Token: token,
		Title: "Giliran Sebentar Lagi",
		Body:  "Bersiap, giliran Anda hampir tiba",
		Data:  map[string]string{"eventId": p.EventID, "queueEntryId": p.QueueEntryID, "type": "TURN_SOON"},
	}
	providerID, serr := u.push.Send(ctx, msg)
	if serr != nil {
		if u.push.IsTokenInvalid(serr) {
			_ = u.subs.Delete(ctx, p.VisitorID, p.EventID)
			_ = u.repo.MarkDead(ctx, sourceMessageID, entity.TypeTurnSoon, serr.Error())
			u.log.Info("turn.soon token invalid, hapus subscription", "messageId", sourceMessageID)
			return nil // R4: invalid token → delete + ack
		}
		_ = u.repo.BumpAttempt(ctx, sourceMessageID, entity.TypeTurnSoon, serr.Error(), u.clock.Now().Add(retryDelay))
		return fmt.Errorf("send turn.soon push: %w", serr)
	}
	return u.repo.MarkSent(ctx, sourceMessageID, entity.TypeTurnSoon, providerID)
}
