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
	qr    outbound.IQREncoder
	clock clock.IClock
	log   *slog.Logger
	// ticketLinkBase is the page that shows the e-tickets (TICKET_LINK_BASE_URL), e.g. https://rusuh.example/tickets.
	ticketLinkBase string
}

// NewNotifyBookingPaid accepts interfaces and returns the concrete usecase.
func NewNotifyBookingPaid(repo outbound.IDeliveryRepository, tmpl outbound.ITemplateRenderer, email outbound.IEmailSender, pii outbound.IPIIProtector, qr outbound.IQREncoder, clk clock.IClock, log *slog.Logger, ticketLinkBase string) *NotifyBookingPaid {
	return &NotifyBookingPaid{repo: repo, tmpl: tmpl, email: email, pii: pii, qr: qr, clock: clk, log: log, ticketLinkBase: ticketLinkBase}
}

// NotifyBookingPaid processes one booking.paid event (idempotent via ClaimDelivery).
func (u *NotifyBookingPaid) NotifyBookingPaid(ctx context.Context, sourceMessageID string, p dto.BookingPaidPayload) error {
	if err := p.Validate(); err != nil {
		return rabbitmq.Permanent(err) // poison → DLQ
	}

	to, rerr := recipient(u.pii, p.BuyerEmail, p.BuyerEmailEnc)
	if rerr != nil {
		return rerr
	}
	claim := mapper.NewEmailDelivery(sourceMessageID, entity.TypeBookingPaid, p.BookingID, u.pii.HMACRecipient(to))
	claimed, err := u.repo.ClaimDelivery(ctx, claim)
	if err != nil {
		return fmt.Errorf("claim delivery: %w", err) // transient (DB) → retry
	}
	if !claimed {
		u.log.Info("booking.paid duplikat, skip", "messageId", sourceMessageID)
		return nil // R8: duplicate → ack
	}

	tickets, images := u.ticketViews(p.Tickets)
	data := dto.EticketData{
		PublicRef:     p.PublicRef,
		EventName:     p.Event.Name,
		EventStartsAt: p.Event.StartsAt,
		EventVenue:    p.Event.Venue,
		Tickets:       tickets,
		TicketURL:     ticketURL(u.ticketLinkBase, p.BookingID, p.TicketLinkToken),
	}
	body, html, rerr := renderBoth(u.tmpl, "eticket", data)
	if rerr != nil {
		_ = u.repo.MarkDead(ctx, sourceMessageID, entity.TypeBookingPaid, rerr.Error())
		return rabbitmq.Permanent(fmt.Errorf("render eticket: %w", rerr))
	}

	subject := fmt.Sprintf("[RBT] E-Tiket Anda — %s", p.Event.Name)
	providerID, serr := u.email.SendEmail(ctx, to, subject, body, html, idempotencyKey(sourceMessageID, entity.TypeBookingPaid), images...)
	if serr != nil {
		recordSendFailure(ctx, u.repo, sourceMessageID, entity.TypeBookingPaid, u.clock.Now(), serr)
		return fmt.Errorf("send eticket email: %w", serr) // transient → retry; permanent stays permanent (DLQ)
	}
	return u.repo.MarkSent(ctx, sourceMessageID, entity.TypeBookingPaid, providerID)
}

// ticketViews builds the template rows and the QR images. A QR that cannot be made leaves that ticket with its code only:
// the buyer still gets the e-ticket.
func (u *NotifyBookingPaid) ticketViews(in []dto.TicketInfo) ([]dto.EticketTicket, []outbound.InlineImage) {
	out := make([]dto.EticketTicket, 0, len(in))
	var images []outbound.InlineImage
	for i, t := range in {
		v := dto.EticketTicket{TicketInfo: t, QRPayload: "RBT:" + t.TicketCode}
		png, err := u.qr.PNG(v.QRPayload)
		if err != nil {
			u.log.Warn("QR tiket gagal dibuat; email dikirim tanpa QR untuk tiket ini", "index", i, "error", err)
		} else {
			v.QRCID = fmt.Sprintf("qr-%d", i+1)
			images = append(images, outbound.InlineImage{CID: v.QRCID, ContentType: "image/png", Data: png})
		}
		out = append(out, v)
	}
	return out, images
}

// ticketURL is {base}/{bookingId}?t={token}; empty unless both a base and the token Booking issued are present, so an
// email never carries a link that cannot work.
func ticketURL(base, bookingID, token string) string {
	if base == "" || token == "" {
		return ""
	}
	return strings.TrimRight(base, "/") + "/" + url.PathEscape(bookingID) + "?t=" + url.QueryEscape(token)
}

// recipient returns the address to send to. The event carries the buyer email as ciphertext; this is the only place it
// is opened. A message produced before PDP-1 may still carry the plaintext field. A ciphertext that does not open is a
// poison message (wrong key or tampering) and goes to the DLQ instead of being retried forever.
func recipient(pii outbound.IPIIProtector, plain, enc string) (string, error) {
	if enc == "" {
		return plain, nil
	}
	email, err := pii.UnsealBuyerEmail(enc)
	if err != nil {
		return "", rabbitmq.Permanent(fmt.Errorf("buka buyerEmailEnc: %w", err))
	}
	return email, nil
}
