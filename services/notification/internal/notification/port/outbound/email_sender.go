package outbound

import "context"

//go:generate go tool mockgen -source=email_sender.go -destination=../../../../_mock/notification/port/outbound/email_sender_mock.go -package=mock_outbound

// IEmailSender sends a single email via SMTP and returns the provider message id.
type IEmailSender interface {
	SendEmail(ctx context.Context, to, subject, htmlBody string) (messageID string, err error)
}
