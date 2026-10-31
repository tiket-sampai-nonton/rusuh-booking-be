// Package usecase orchestrates the Notification domain. One usecase per file.
package usecase

import (
	"context"
	"fmt"
	"log/slog"
	"net/url"
	"strings"
	"time"

	"github.com/tiket-sampai-nonton/rusuh-booking-be/pkg/clock"
	"github.com/tiket-sampai-nonton/rusuh-booking-be/pkg/rabbitmq"

	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/notification/internal/notification/domain/dto"
	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/notification/internal/notification/domain/entity"
	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/notification/internal/notification/domain/mapper"
	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/notification/internal/notification/port/outbound"
)

// retryDelay is the backoff recorded on next_attempt_at when a transient send fails. The real
// re-delivery timing is owned by the RabbitMQ retry queue; this is for the status row only.
const retryDelay = 5 * time.Second

// NotifyBookingPaid implements inbound.INotifyBookingPaid: render the e-ticket email and send it.
type NotifyBookingPaid struct {
	repo  outbound.IDeliveryRepository
	tmpl  outbound.ITemplateRenderer
	email outbound.IEmailSender
	pii   outbound.IPIIProtector
	clock clock.IClock
	log   *slog.Logger
	// ticketLinkBase is the page that shows the e-tickets (TICKET_LINK_BASE_URL), e.g. https://rusuh.example/tickets.
	ticketLinkBase string
}

// NewNotifyBookingPaid accepts interfaces and returns the concrete usecase.
func NewNotifyBookingPaid(repo outbound.IDeliveryRepository, tmpl outbound.ITemplateRenderer, email outbound.IEmailSender, pii outbound.IPIIProtector, clk clock.IClock, log *slog.Logger, ticketLinkBase string) *NotifyBookingPaid {
	return &NotifyBookingPaid{repo: repo, tmpl: tmpl, email: email, pii: pii, clock: clk, log: log, ticketLinkBase: ticketLinkBase}
}

// NotifyBookingPaid processes one booking.paid event (idempotent via ClaimDelivery).
func (u *NotifyBookingPaid) NotifyBookingPaid(ctx context.Context, sourceMessageID string, p dto.BookingPaidPayload) error {
	if err := p.Validate(); err != nil {
		return rabbitmq.Permanent(err) // poison → DLQ
	}

	claim := mapper.NewEmailDelivery(sourceMessageID, entity.TypeBookingPaid, p.BookingID, u.pii.HMACRecipient(p.BuyerEmail))
	claimed, err := u.repo.ClaimDelivery(ctx, claim)
	if err != nil {
		return fmt.Errorf("claim delivery: %w", err) // transient (DB) → retry
	}
	if !claimed {
		u.log.Info("booking.paid duplikat, skip", "messageId", sourceMessageID)
		return nil // R8: duplicate → ack
	}

	data := dto.EticketData{
		PublicRef:     p.PublicRef,
		EventName:     p.Event.Name,
		EventStartsAt: p.Event.StartsAt,
		EventVenue:    p.Event.Venue,
		Tickets:       p.Tickets,
		TicketURL:     ticketURL(u.ticketLinkBase, p.BookingID, p.TicketLinkToken),
	}
	body, html, rerr := renderBoth(u.tmpl, "eticket", data)
	if rerr != nil {
		_ = u.repo.MarkDead(ctx, sourceMessageID, entity.TypeBookingPaid, rerr.Error())
		return rabbitmq.Permanent(fmt.Errorf("render eticket: %w", rerr))
	}

	subject := fmt.Sprintf("[RBT] E-Tiket Anda — %s", p.Event.Name)
	providerID, serr := u.email.SendEmail(ctx, p.BuyerEmail, subject, body, html, idempotencyKey(sourceMessageID, entity.TypeBookingPaid))
	if serr != nil {
		recordSendFailure(ctx, u.repo, sourceMessageID, entity.TypeBookingPaid, u.clock.Now(), serr)
		return fmt.Errorf("send eticket email: %w", serr) // transient → retry; permanent stays permanent (DLQ)
	}
	return u.repo.MarkSent(ctx, sourceMessageID, entity.TypeBookingPaid, providerID)
}

// ticketURL is {base}/{bookingId}?t={token}; empty unless both a base and the token Booking issued are present, so an
// email never carries a link that cannot work.
func ticketURL(base, bookingID, token string) string {
	if base == "" || token == "" {
		return ""
	}
	return strings.TrimRight(base, "/") + "/" + url.PathEscape(bookingID) + "?t=" + url.QueryEscape(token)
}
