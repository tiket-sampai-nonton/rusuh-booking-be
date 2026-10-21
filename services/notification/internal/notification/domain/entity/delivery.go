// Package entity holds the pure domain model of the Notification Service (standard library only).
package entity

import "time"

// NotificationType is the kind of notification tracked in the deliveries table.
// Values mirror the migration CHECK constraint (0001_init): email types and push types.
type NotificationType string

// Notification types (0001_init CHECK constraint). OTP is NOT tracked here (time-sensitive, no retry).
const (
	TypeBookingPaid    NotificationType = "BOOKING_PAID"
	TypeBookingExpired NotificationType = "BOOKING_EXPIRED"
	TypeBookingReview  NotificationType = "BOOKING_REVIEW"
	TypeTurnSoon       NotificationType = "TURN_SOON"
	TypeTurnArrived    NotificationType = "TURN_ARRIVED"
)

// Channel is the delivery medium.
type Channel string

// Channels (0001_init CHECK constraint).
const (
	ChannelEmail Channel = "EMAIL"
	ChannelPush  Channel = "PUSH"
)

// DeliveryStatus is the lifecycle state of one delivery row.
type DeliveryStatus string

// Delivery states (0001_init CHECK constraint).
const (
	StatusPending DeliveryStatus = "PENDING"
	StatusSending DeliveryStatus = "SENDING"
	StatusSent    DeliveryStatus = "SENT"
	StatusRetry   DeliveryStatus = "RETRY"
	StatusDead    DeliveryStatus = "DEAD"
)

// Delivery is one tracked notification send. The row is both the dedup key
// (UNIQUE source_message_id + notification_type) and the status record (§9.8, no inbox table).
type Delivery struct {
	ID                string
	SourceMessageID   string
	Type              NotificationType
	Channel           Channel
	BookingID         *string
	QueueEntryID      *string
	RecipientHash     []byte
	Status            DeliveryStatus
	Attempts          int
	ProviderMessageID *string
	LastError         *string
	NextAttemptAt     time.Time
	SentAt            *time.Time
	CreatedAt         time.Time
	ExpiresAt         *time.Time
}

// IsEmail reports whether the delivery is an email channel notification.
func (d Delivery) IsEmail() bool { return d.Channel == ChannelEmail }

// IsPush reports whether the delivery is a push channel notification.
func (d Delivery) IsPush() bool { return d.Channel == ChannelPush }
