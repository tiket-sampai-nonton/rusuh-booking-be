package usecase

import (
	"context"

	"github.com/tiket-sampai-nonton/rusuh-booking-be/pkg/clock"

	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/booking/internal/booking/port/inbound"
	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/booking/internal/booking/port/outbound"
)

// PublishEvent switches an event between draft and published. Publishing is allowed at any time and is idempotent;
// hiding an event again is only allowed before its sales window opens, so buyers who already hold a booking are
// never left with an event that disappeared.
type PublishEvent struct {
	events outbound.IEventRepo
	repo   outbound.IEventPublicationRepo
	clock  clock.IClock
}

// NewPublishEvent wires the usecase.
func NewPublishEvent(events outbound.IEventRepo, repo outbound.IEventPublicationRepo, clk clock.IClock) *PublishEvent {
	return &PublishEvent{events: events, repo: repo, clock: clk}
}

// Publish makes the event visible and sellable.
func (uc *PublishEvent) Publish(ctx context.Context, eventID string) error {
	if !isUUID(eventID) {
		return inbound.Validation("eventId", "eventId harus UUID")
	}
	return uc.repo.SetPublished(ctx, eventID, true)
}

// Unpublish hides the event again; rejected (EVENT_RULES_LOCKED) once the sales window has opened.
func (uc *PublishEvent) Unpublish(ctx context.Context, eventID string) error {
	if !isUUID(eventID) {
		return inbound.Validation("eventId", "eventId harus UUID")
	}
	event, err := uc.events.GetByID(ctx, eventID)
	if err != nil {
		return err
	}
	if !uc.clock.Now().Before(event.SalesStartsAt) {
		return inbound.ErrRulesLocked
	}
	return uc.repo.SetPublished(ctx, eventID, false)
}
