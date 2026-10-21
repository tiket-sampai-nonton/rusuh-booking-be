package outbound

import "context"

//go:generate go tool mockgen -source=push_sender.go -destination=../../../../_mock/notification/port/outbound/push_sender_mock.go -package=mock_outbound

// PushMessage is a push notification to send to one device token.
type PushMessage struct {
	Token string
	Title string
	Body  string
	Data  map[string]string
}

// IPushSender sends push notifications via FCM HTTP v1.
type IPushSender interface {
	// Send delivers one push and returns the provider message id.
	Send(ctx context.Context, msg PushMessage) (messageID string, err error)
	// IsTokenInvalid reports whether err means the token is permanently invalid (UNREGISTERED /
	// INVALID_ARGUMENT) so the caller deletes the subscription instead of retrying.
	IsTokenInvalid(err error) bool
}
