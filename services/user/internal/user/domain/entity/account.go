package entity

import "time"

// Account status values.
const (
	AccountActive   = "ACTIVE"
	AccountDisabled = "DISABLED"
)

// Account is an optional buyer account (ADR-020). Data PII hanya hadir dalam bentuk tersamar di entity.
type Account struct {
	ID          string
	EmailMasked string
	FullName    string // kosong bila belum diisi
	NIKMasked   string // kosong bila belum diisi
	Status      string
	CreatedAt   time.Time
	LastLoginAt *time.Time
}

// HasNIK reports whether a NIK is stored.
func (a Account) HasNIK() bool { return a.NIKMasked != "" }

// Active reports whether the account may log in.
func (a Account) Active() bool { return a.Status == AccountActive }

// NewAccount is what the repository needs to create an account on first login.
type NewAccount struct {
	EmailHash   []byte
	EmailEnc    []byte
	EmailMasked string
}

// ProfileUpdate carries the fields to change. Nil berarti tidak diubah.
type ProfileUpdate struct {
	FullName  *string
	NIKHash   []byte
	NIKEnc    []byte
	NIKMasked string
}

// Session is a refresh-token session.
type Session struct {
	ID        string
	AccountID string
	ExpiresAt time.Time
	RevokedAt *time.Time
}

// Revoked reports whether the session was revoked (logout atau rotasi).
func (s Session) Revoked() bool { return s.RevokedAt != nil }

// Expired reports whether the session expired at now.
func (s Session) Expired(now time.Time) bool { return !now.Before(s.ExpiresAt) }

// NewSession is what the repository needs to store a session.
type NewSession struct {
	AccountID        string
	RefreshTokenHash []byte
	UserAgent        string
	ExpiresAt        time.Time
}

// OTPChallenge is the stored state of an issued OTP.
type OTPChallenge struct {
	CodeHash []byte
	Attempts int
}

// Login audit outcomes (users.login_audit.outcome).
const (
	AuditOTPSent        = "OTP_SENT"
	AuditOTPRateLimited = "OTP_RATE_LIMITED"
	AuditLoginOK        = "LOGIN_OK"
	AuditOTPInvalid     = "OTP_INVALID"
	AuditOTPExpired     = "OTP_EXPIRED"
	AuditOTPLocked      = "OTP_LOCKED"
	AuditRefreshOK      = "REFRESH_OK"
	AuditRefreshReused  = "REFRESH_REUSED"
	AuditLogout         = "LOGOUT"
)

// LoginEvent is one audit row.
type LoginEvent struct {
	AccountID string // kosong bila belum diketahui
	EmailHash []byte
	Outcome   string
	ClientIP  string
}
