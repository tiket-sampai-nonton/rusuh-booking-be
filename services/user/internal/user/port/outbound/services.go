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

// IMailer sends the OTP email.
type IMailer interface {
	SendOTP(ctx context.Context, to, code string, ttl time.Duration) error
}

// ITokenIssuer signs access tokens.
type ITokenIssuer interface {
	IssueAccess(accountID string, now time.Time) (token string, expiresAt time.Time, err error)
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
	HashNIK(nik string) []byte
	SealNIK(nik string) ([]byte, error)
	HashOTP(emailHash []byte, code string) []byte
	HashRefreshToken(raw string) []byte
}
