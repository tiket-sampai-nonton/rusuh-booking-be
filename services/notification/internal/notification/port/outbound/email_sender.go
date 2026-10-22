package outbound

import "context"

//go:generate go tool mockgen -source=email_sender.go -destination=../../../../_mock/notification/port/outbound/email_sender_mock.go -package=mock_outbound

// IEmailSender sends a single plain-text email and returns the provider message id.
// idempotencyKey (source message id + notification type) lets a provider drop a repeated send;
// providers without that feature (SMTP/Mailpit) ignore it. A rabbitmq.Permanent error means retrying
// cannot help (rejected sender, bad address, invalid key).
type IEmailSender interface {
	SendEmail(ctx context.Context, to, subject, body, idempotencyKey string) (messageID string, err error)
}
