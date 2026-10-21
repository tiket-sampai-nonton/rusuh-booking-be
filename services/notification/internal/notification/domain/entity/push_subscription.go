package entity

import "time"

// Platform is the mobile OS of a push subscription (migration CHECK: ANDROID | IOS).
type Platform string

// Platforms supported for push (0001_init CHECK constraint; no WEB).
const (
	PlatformAndroid Platform = "ANDROID"
	PlatformIOS     Platform = "IOS"
)

// Valid reports whether p is a recognised platform.
func (p Platform) Valid() bool { return p == PlatformAndroid || p == PlatformIOS }

// PushSubscription is a device FCM token registered for a (visitor, event) pair.
// The token is stored encrypted (push_token_enc); never kept in plaintext.
type PushSubscription struct {
	VisitorID    string
	EventID      string
	Platform     Platform
	PushTokenEnc []byte
	CreatedAt    time.Time
	ExpiresAt    time.Time
}
