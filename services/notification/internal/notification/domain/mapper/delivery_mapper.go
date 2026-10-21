// Package mapper converts between domain entities, event payloads, and transport/template DTOs.
package mapper

import (
	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/notification/internal/notification/domain/dto"
	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/notification/internal/notification/domain/entity"
)

// NewEmailDelivery builds an email Delivery claim row for a booking-sourced notification.
func NewEmailDelivery(sourceMessageID string, t entity.NotificationType, bookingID string, recipientHash []byte) entity.Delivery {
	bid := bookingID
	return entity.Delivery{
		SourceMessageID: sourceMessageID,
		Type:            t,
		Channel:         entity.ChannelEmail,
		BookingID:       &bid,
		RecipientHash:   recipientHash,
		Status:          entity.StatusSending,
	}
}

// NewPushDelivery builds a push Delivery claim row for a queue-sourced notification.
func NewPushDelivery(sourceMessageID string, t entity.NotificationType, queueEntryID string, recipientHash []byte) entity.Delivery {
	qid := queueEntryID
	return entity.Delivery{
		SourceMessageID: sourceMessageID,
		Type:            t,
		Channel:         entity.ChannelPush,
		QueueEntryID:    &qid,
		RecipientHash:   recipientHash,
		Status:          entity.StatusSending,
	}
}

// ToSubscription builds a PushSubscription entity from a validated subscribe request.
func ToSubscription(visitorID string, req dto.SubscribeRequest, tokenEnc []byte) entity.PushSubscription {
	return entity.PushSubscription{
		VisitorID:    visitorID,
		EventID:      req.EventID,
		Platform:     entity.Platform(req.Platform),
		PushTokenEnc: tokenEnc,
	}
}
