package usecase

import (
	"context"
	"time"

	"github.com/tiket-sampai-nonton/rusuh-booking-be/pkg/rabbitmq"

	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/notification/internal/notification/domain/entity"
	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/notification/internal/notification/port/outbound"
)

// otpIdempotencySuffix completes the OTP email idempotency key (OTP has no deliveries row).
const otpIdempotencySuffix = "OTP_LOGIN"

// idempotencyKey is what a provider uses to drop a repeated send: source message id + notification type.
func idempotencyKey(sourceMessageID string, t entity.NotificationType) string {
	return sourceMessageID + ":" + string(t)
}

// recordSendFailure updates the delivery after a failed email send. A permanent error (the provider
// rejected the request; the message goes to the DLQ) ends as DEAD so the row matches the DLQ; a
// transient error keeps the row in RETRY. The error text is stored as last_error, so adapters must
// keep secrets and addresses out of it.
func recordSendFailure(ctx context.Context, repo outbound.IDeliveryRepository, sourceMessageID string, t entity.NotificationType, now time.Time, serr error) {
	if rabbitmq.IsPermanent(serr) {
		_ = repo.MarkDead(ctx, sourceMessageID, t, serr.Error())
		return
	}
	_ = repo.BumpAttempt(ctx, sourceMessageID, t, serr.Error(), now.Add(retryDelay))
}
