package outbound

import (
	"context"
	"time"

	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/user/internal/user/domain/entity"
)

//go:generate go tool mockgen -source=services.go -destination=../../../../_mock/user/port/outbound/services_mock.go -package=mock_outbound

// IOTPStore keeps OTP challenges with a TTL (Valkey). Kehilangan data hanya berarti pengguna meminta kode baru.
type IOTPStore interface {
	// InCooldown reports whether the resend cooldown for key is active. Read only; tidak memakai kuota.
	InCooldown(ctx context.Context, key string) (bool, error)
	// Start atomically sets the cooldown (NX), the code hash and resets the attempt counter; started=false bila cooldown aktif.
	Start(ctx context.Context, key string, codeHash []byte, ttl, cooldown time.Duration) (started bool, err error)
	// Verify atomically counts the attempt and compares; pada kecocokan atau lock challenge dihapus (sekali pakai).
	Verify(ctx context.Context, key string, candidateHash []byte, maxAttempts int) (entity.OTPAttempt, error)
	// Cancel deletes challenge, counter AND cooldown (kode tidak berhasil dikirim).
	Cancel(ctx context.Context, key string) error
}

// RateTicket identifies the exact bucket (full Valkey key) that one Allow call incremented.
type RateTicket struct {
	Allowed bool
	Bucket  string // opaque; kosong bila Allow mengembalikan error
}

// IRateLimiter is a fixed-window counter.
type IRateLimiter interface {
	// Allow increments the current window counter and reports whether it is within limit.
	Allow(ctx context.Context, key string, limit int, window time.Duration) (RateTicket, error)
	// Release gives back one unit of the bucket named by the ticket (no-op bila bucket hilang atau 0).
	Release(ctx context.Context, t RateTicket) error
}

// IOTPPublisher publishes otp.requested events to the broker so Notification Service
// sends the email. Email dan code dienkripsi AES-256-GCM sebelum dipublish (SRS-F-27-12).
type IOTPPublisher interface {
	PublishOTP(ctx context.Context, emailEnc, codeEnc []byte, emailHash []byte, now time.Time, expiresAt time.Time) error
}

// ITokenIssuer signs access tokens.
type ITokenIssuer interface {
	// IssueAccess signs an access token; sessionID goes into claim "sid" so logout can revoke it before exp.
	IssueAccess(accountID, sessionID, email string, nikHash []byte, nikMasked string, now time.Time) (token string, expiresAt time.Time, err error)
}

// ISecretGenerator produces random secrets.
type ISecretGenerator interface {
	OTPCode() (string, error)
	RefreshToken() (string, error)
	// SessionID returns a random UUIDv4 so the access token (claim sid) can be issued before the session row is committed.
	SessionID() (string, error)
}

// IPIIProtector hashes and encrypts PII and secrets.
type IPIIProtector interface {
	HashEmail(email string) []byte
	SealEmail(email string) ([]byte, error)
	UnsealEmail(enc []byte) (string, error)
	SealOTP(code string) ([]byte, error)
	HashNIK(nik string) []byte
	SealNIK(nik string) ([]byte, error)
	UnsealNIK(enc []byte) (string, error)
	HashOTP(emailHash []byte, code string) []byte
	HashRefreshToken(raw string) []byte
}
