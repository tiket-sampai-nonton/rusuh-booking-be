package dto

import (
	"errors"
	"fmt"
)

// MaxPushTokenLen is the maximum accepted FCM device token length (contract notification.yaml).
const MaxPushTokenLen = 4096

// SubscribeRequest is the POST /api/v1/push-subscriptions body.
type SubscribeRequest struct {
	EventID   string `json:"eventId"`
	Platform  string `json:"platform"`
	PushToken string `json:"pushToken"`
}

// Validate checks the field-level rules; returns a map of field→message for VALIDATION_ERROR details.
func (r SubscribeRequest) Validate() map[string]string {
	details := map[string]string{}
	if r.EventID == "" {
		details["eventId"] = "wajib diisi"
	}
	switch r.Platform {
	case "ANDROID", "IOS":
	default:
		details["platform"] = "harus ANDROID atau IOS"
	}
	if r.PushToken == "" {
		details["pushToken"] = "wajib diisi"
	} else if len(r.PushToken) > MaxPushTokenLen {
		details["pushToken"] = fmt.Sprintf("melebihi %d karakter", MaxPushTokenLen)
	}
	if len(details) == 0 {
		return nil
	}
	return details
}

// ErrValidation is returned by the usecase when SubscribeRequest fails validation.
var ErrValidation = errors.New("validasi gagal")
