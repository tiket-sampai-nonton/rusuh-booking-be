package outbound

import "context"

//go:generate go tool mockgen -source=email_sender.go -destination=../../../../_mock/notification/port/outbound/email_sender_mock.go -package=mock_outbound

// InlineImage is an image shown inside the HTML body through <img src="cid:CID">. Mail clients block data: URIs and
// remote images by default, so a QR code travels with the message instead.
type InlineImage struct {
	CID         string
	ContentType string
	Data        []byte
}

// IEmailSender sends a single plain-text email and returns the provider message id.
// idempotencyKey (source message id + notification type) lets a provider drop a repeated send;
// providers without that feature (SMTP/Mailpit) ignore it. A rabbitmq.Permanent error means retrying
// cannot help (rejected sender, bad address, invalid key).
type IEmailSender interface {
	SendEmail(ctx context.Context, to, subject, text, html, idempotencyKey string, images ...InlineImage) (messageID string, err error)
}
