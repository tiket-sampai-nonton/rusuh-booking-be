// Package rabbitmq implements the IOTPPublisher driven port using pkg/rabbitmq.
// OTP email dan kode dienkripsi AES-256-GCM sebelum dipublish sehingga tidak ada
// plaintext secret di broker (SRS-F-27-12, SDD §5c).
package rabbitmq

import (
	"context"
	"encoding/base64"
	"fmt"
	"time"

	pkgrabbitmq "github.com/tiket-sampai-nonton/rusuh-booking-be/pkg/rabbitmq"
)

const (
	routingKeyOTP = "otp.requested"
	producerName  = "user"
	schemaVersion = 1
)

// otpPayload is the otp.requested event payload schema (SDD §5c).
type otpPayload struct {
	EmailEnc  string `json:"emailEnc"`  // base64url AES-256-GCM
	CodeEnc   string `json:"codeEnc"`   // base64url AES-256-GCM
	ExpiresAt string `json:"expiresAt"` // RFC3339
}

// OTPPublisher implements outbound.IOTPPublisher.
type OTPPublisher struct {
	pub *pkgrabbitmq.Publisher
}

// NewOTPPublisher wraps an existing confirm-mode publisher.
func NewOTPPublisher(pub *pkgrabbitmq.Publisher) *OTPPublisher {
	return &OTPPublisher{pub: pub}
}

// PublishOTP publishes an otp.requested event with encrypted email and code.
func (p *OTPPublisher) PublishOTP(ctx context.Context, emailEnc, codeEnc []byte, expiresAt time.Time) error {
	payload := otpPayload{
		EmailEnc:  base64.RawURLEncoding.EncodeToString(emailEnc),
		CodeEnc:   base64.RawURLEncoding.EncodeToString(codeEnc),
		ExpiresAt: expiresAt.UTC().Format(time.RFC3339),
	}
	env, err := pkgrabbitmq.NewEnvelope(routingKeyOTP, producerName, "", "", payload, expiresAt)
	if err != nil {
		return fmt.Errorf("otp publisher: build envelope: %w", err)
	}
	if err := p.pub.Publish(ctx, routingKeyOTP, env); err != nil {
		return fmt.Errorf("otp publisher: publish: %w", err)
	}
	return nil
}
