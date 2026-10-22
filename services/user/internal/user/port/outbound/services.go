package outbound

import (
	"context"
	"time"

	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/user/internal/user/domain/entity"
)

//go:generate go tool mockgen -source=services.go -destination=../../../../_mock/user/port/outbound/services_mock.go -package=mock_outbound

// IOTPStore keeps OTP challenges with a TTL (Valkey). Kehilangan data hanya berarti pengguna meminta kode baru.
type IOTPStore interface {
	// Start stores a challenge unless the resend cooldown for key is still active (started=false).
	Start(ctx context.Context, key string, codeHash []byte, ttl, cooldown time.Duration) (started bool, err error)
	// Attempt increments the attempt counter and returns the challenge; found=false bila tidak ada/kedaluwarsa.
	Attempt(ctx context.Context, key string) (ch entity.OTPChallenge, found bool, err error)
	Clear(ctx context.Context, key string) error
}

// IRateLimiter is a fixed-window counter.
type IRateLimiter interface {
	Allow(ctx context.Context, key string, limit int, window time.Duration) (bool, error)
}

// IOTPPublisher publishes otp.requested events to the broker so Notification Service
// sends the email. Email dan code dienkripsi AES-256-GCM sebelum dipublish (SRS-F-27-12).
type IOTPPublisher interface {
	PublishOTP(ctx context.Context, emailEnc, codeEnc []byte, emailHash []byte, now time.Time, expiresAt time.Time) error
}

// ITokenIssuer signs access tokens.
type ITokenIssuer interface {
	IssueAccess(accountID, email string, nikHash []byte, now time.Time) (token string, expiresAt time.Time, err error)
}

// ISecretGenerator produces random secrets.
type ISecretGenerator interface {
	OTPCode() (string, error)
	RefreshToken() (string, error)
}

// IPIIProtector hashes and encrypts PII and secrets.
type IPIIProtector interface {
	HashEmail(email string) []byte
	SealEmail(email string) ([]byte, error)
	UnsealEmail(enc []byte) (string, error)
	SealOTP(code string) ([]byte, error)
	HashNIK(nik string) []byte
	SealNIK(nik string) ([]byte, error)
	HashOTP(emailHash []byte, code string) []byte
	HashRefreshToken(raw string) []byte
}
