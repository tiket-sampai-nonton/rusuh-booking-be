package usecase

import (
	"context"
	"crypto/subtle"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"strconv"
	"time"

	"github.com/tiket-sampai-nonton/rusuh-booking-be/pkg/apperror"
	"github.com/tiket-sampai-nonton/rusuh-booking-be/pkg/clock"

	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/user/internal/user/domain/dto"
	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/user/internal/user/domain/entity"
	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/user/internal/user/domain/mapper"
	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/user/internal/user/domain/valueobject"
	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/user/internal/user/port/inbound"
	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/user/internal/user/port/outbound"
)

// AuthSettings are the OTP and session limits (dari config OTP_* dan TOKEN_REFRESH_TTL).
type AuthSettings struct {
	OTPTTL          time.Duration
	ResendCooldown  time.Duration
	MaxAttempts     int
	MaxPerEmailHour int
	MaxPerIPHour    int
	RefreshTTL      time.Duration
}

// AuthDeps are the driven ports of the auth usecase.
type AuthDeps struct {
	Accounts  outbound.IAccountRepository
	Sessions  outbound.ISessionRepository
	Audit     outbound.ILoginAudit
	OTP       outbound.IOTPStore
	Limiter   outbound.IRateLimiter
	Publisher outbound.IOTPPublisher
	Tokens    outbound.ITokenIssuer
	Secrets   outbound.ISecretGenerator
	PII       outbound.IPIIProtector
	Clock     clock.IClock
	Logger    *slog.Logger
}

// Auth implements inbound.IAuthUsecase.
type Auth struct {
	d AuthDeps
	s AuthSettings
}

// NewAuth accepts interfaces and returns the concrete usecase.
func NewAuth(d AuthDeps, s AuthSettings) *Auth { return &Auth{d: d, s: s} }

var _ inbound.IAuthUsecase = (*Auth)(nil)

// RequestOTP sends a 6-digit code. Respons sama untuk email terdaftar maupun belum (anti enumerasi).
func (u *Auth) RequestOTP(ctx context.Context, req dto.RequestOTPRequest, client inbound.ClientInfo) (dto.RequestOTPResponse, error) {
	email, err := valueobject.NewEmail(req.Email)
	if err != nil {
		return dto.RequestOTPResponse{}, inbound.Validation("email", err)
	}
	emailHash := u.d.PII.HashEmail(email.String())
	key := hex.EncodeToString(emailHash)

	for _, l := range []struct {
		key   string
		limit int
	}{
		{"otp:email:" + key, u.s.MaxPerEmailHour},
		{"otp:ip:" + client.IP, u.s.MaxPerIPHour},
	} {
		ok, err := u.d.Limiter.Allow(ctx, l.key, l.limit, time.Hour)
		if err != nil {
			return dto.RequestOTPResponse{}, apperror.Internal(fmt.Errorf("rate limit: %w", err))
		}
		if !ok {
			u.audit(ctx, entity.LoginEvent{EmailHash: emailHash, Outcome: entity.AuditOTPRateLimited, ClientIP: client.IP})
			return dto.RequestOTPResponse{}, inbound.ErrRateLimited
		}
	}

	code, err := u.d.Secrets.OTPCode()
	if err != nil {
		return dto.RequestOTPResponse{}, apperror.Internal(err)
	}
	started, err := u.d.OTP.Start(ctx, key, u.d.PII.HashOTP(emailHash, code), u.s.OTPTTL, u.s.ResendCooldown)
	if err != nil {
		return dto.RequestOTPResponse{}, apperror.Internal(fmt.Errorf("simpan otp: %w", err))
	}
	if !started {
		return dto.RequestOTPResponse{}, inbound.ErrOTPResendTooSoon.WithDetails(map[string]string{
			"retryAfterSeconds": strconv.Itoa(int(u.s.ResendCooldown.Seconds()))})
	}
	// Enkripsi email dan kode sebelum dipublish — OTP adalah secret, tidak boleh plaintext di broker (SRS-F-27-12).
	emailEnc, err := u.d.PII.SealEmail(email.String())
	if err != nil {
		_ = u.d.OTP.Clear(ctx, key)
		return dto.RequestOTPResponse{}, apperror.Internal(fmt.Errorf("seal email: %w", err))
	}
	codeEnc, err := u.d.PII.SealOTP(code)
	if err != nil {
		_ = u.d.OTP.Clear(ctx, key)
		return dto.RequestOTPResponse{}, apperror.Internal(fmt.Errorf("seal otp: %w", err))
	}
	expiresAt := u.d.Clock.Now().Add(u.s.OTPTTL)
	if err := u.d.Publisher.PublishOTP(ctx, emailEnc, codeEnc, expiresAt); err != nil {
		if cerr := u.d.OTP.Clear(ctx, key); cerr != nil {
			u.d.Logger.Warn("gagal menghapus otp setelah publish gagal", "error", cerr)
		}
		u.d.Logger.Error("publish otp.requested gagal", "error", err)
		return dto.RequestOTPResponse{}, inbound.ErrPublishFailed
	}
	u.audit(ctx, entity.LoginEvent{EmailHash: emailHash, Outcome: entity.AuditOTPSent, ClientIP: client.IP})
	return dto.RequestOTPResponse{ExpiresInSeconds: int(u.s.OTPTTL.Seconds()), ResendAfterSeconds: int(u.s.ResendCooldown.Seconds())}, nil
}

// VerifyOTP checks the code, creates the account on first login, and opens a session.
func (u *Auth) VerifyOTP(ctx context.Context, req dto.VerifyOTPRequest, client inbound.ClientInfo) (dto.TokenResponse, error) {
	email, err := valueobject.NewEmail(req.Email)
	if err != nil {
		return dto.TokenResponse{}, inbound.Validation("email", err)
	}
	code, err := valueobject.NewOTPCode(req.Code)
	if err != nil {
		return dto.TokenResponse{}, inbound.Validation("code", err)
	}
	emailHash := u.d.PII.HashEmail(email.String())
	key := hex.EncodeToString(emailHash)

	ch, found, err := u.d.OTP.Attempt(ctx, key)
	if err != nil {
		return dto.TokenResponse{}, apperror.Internal(fmt.Errorf("baca otp: %w", err))
	}
	if !found {
		u.audit(ctx, entity.LoginEvent{EmailHash: emailHash, Outcome: entity.AuditOTPExpired, ClientIP: client.IP})
		return dto.TokenResponse{}, inbound.ErrOTPExpired
	}
	if ch.Attempts > u.s.MaxAttempts {
		_ = u.d.OTP.Clear(ctx, key)
		u.audit(ctx, entity.LoginEvent{EmailHash: emailHash, Outcome: entity.AuditOTPLocked, ClientIP: client.IP})
		return dto.TokenResponse{}, inbound.ErrOTPLocked
	}
	if subtle.ConstantTimeCompare(ch.CodeHash, u.d.PII.HashOTP(emailHash, code.String())) != 1 {
		u.audit(ctx, entity.LoginEvent{EmailHash: emailHash, Outcome: entity.AuditOTPInvalid, ClientIP: client.IP})
		left := u.s.MaxAttempts - ch.Attempts
		if left < 0 {
			left = 0
		}
		return dto.TokenResponse{}, inbound.ErrOTPInvalid.WithDetails(map[string]string{"attemptsLeft": strconv.Itoa(left)})
	}
	if err := u.d.OTP.Clear(ctx, key); err != nil {
		return dto.TokenResponse{}, apperror.Internal(fmt.Errorf("hapus otp: %w", err))
	}

	acc, ok, err := u.d.Accounts.FindByEmailHash(ctx, emailHash)
	if err != nil {
		return dto.TokenResponse{}, apperror.Internal(err)
	}
	if !ok {
		enc, err := u.d.PII.SealEmail(email.String())
		if err != nil {
			return dto.TokenResponse{}, apperror.Internal(err)
		}
		acc, err = u.d.Accounts.Create(ctx, entity.NewAccount{EmailHash: emailHash, EmailEnc: enc, EmailMasked: maskEmail(email.String())})
		if err != nil {
			return dto.TokenResponse{}, apperror.Internal(err)
		}
	}
	if !acc.Active() {
		return dto.TokenResponse{}, inbound.ErrAccountDisabled
	}
	now := u.d.Clock.Now()
	if err := u.d.Accounts.TouchLogin(ctx, acc.ID, now); err != nil {
		return dto.TokenResponse{}, apperror.Internal(err)
	}
	resp, err := u.openSession(ctx, acc, email.String(), client, now, "")
	if err != nil {
		return dto.TokenResponse{}, err
	}
	u.audit(ctx, entity.LoginEvent{AccountID: acc.ID, EmailHash: emailHash, Outcome: entity.AuditLoginOK, ClientIP: client.IP})
	return resp, nil
}

// Refresh rotates the refresh token. Token lama yang dipakai ulang mencabut semua sesi akun (deteksi pencurian).
func (u *Auth) Refresh(ctx context.Context, req dto.RefreshRequest, client inbound.ClientInfo) (dto.TokenResponse, error) {
	if req.RefreshToken == "" {
		return dto.TokenResponse{}, inbound.ErrSessionInvalid
	}
	sess, found, err := u.d.Sessions.FindByTokenHash(ctx, u.d.PII.HashRefreshToken(req.RefreshToken))
	if err != nil {
		return dto.TokenResponse{}, apperror.Internal(err)
	}
	if !found {
		return dto.TokenResponse{}, inbound.ErrSessionInvalid
	}
	now := u.d.Clock.Now()
	if sess.Revoked() {
		if err := u.d.Sessions.RevokeAllForAccount(ctx, sess.AccountID, now); err != nil {
			return dto.TokenResponse{}, apperror.Internal(err)
		}
		u.audit(ctx, entity.LoginEvent{AccountID: sess.AccountID, Outcome: entity.AuditRefreshReused, ClientIP: client.IP})
		return dto.TokenResponse{}, inbound.ErrSessionInvalid
	}
	if sess.Expired(now) {
		return dto.TokenResponse{}, inbound.ErrSessionInvalid
	}
	acc, err := u.d.Accounts.GetByID(ctx, sess.AccountID)
	if err != nil {
		if errors.Is(err, entity.ErrNotFound) {
			return dto.TokenResponse{}, inbound.ErrSessionInvalid
		}
		return dto.TokenResponse{}, apperror.Internal(err)
	}
	if !acc.Active() {
		return dto.TokenResponse{}, inbound.ErrAccountDisabled
	}

	// Fetch email for token claims (Refresh flow doesn't have email in context)
	emailEnc, err := u.d.Accounts.GetEmailEncByID(ctx, acc.ID)
	if err != nil {
		return dto.TokenResponse{}, apperror.Internal(fmt.Errorf("fetch email: %w", err))
	}
	email, err := u.d.PII.UnsealEmail(emailEnc)
	if err != nil {
		return dto.TokenResponse{}, apperror.Internal(fmt.Errorf("unseal email: %w", err))
	}

	resp, err := u.openSession(ctx, acc, email, client, now, sess.ID)
	if err != nil {
		return dto.TokenResponse{}, err
	}
	u.audit(ctx, entity.LoginEvent{AccountID: acc.ID, Outcome: entity.AuditRefreshOK, ClientIP: client.IP})
	return resp, nil
}

// Logout revokes the session. Idempoten: token tidak dikenal tetap dijawab sukses.
func (u *Auth) Logout(ctx context.Context, req dto.RefreshRequest, client inbound.ClientInfo) error {
	if req.RefreshToken == "" {
		return nil
	}
	sess, found, err := u.d.Sessions.FindByTokenHash(ctx, u.d.PII.HashRefreshToken(req.RefreshToken))
	if err != nil {
		return apperror.Internal(err)
	}
	if !found || sess.Revoked() {
		return nil
	}
	if err := u.d.Sessions.Revoke(ctx, sess.ID, u.d.Clock.Now()); err != nil {
		return apperror.Internal(err)
	}
	u.audit(ctx, entity.LoginEvent{AccountID: sess.AccountID, Outcome: entity.AuditLogout, ClientIP: client.IP})
	return nil
}

// openSession issues a refresh token (baru, atau rotasi dari rotateFrom) and an access token.
// email param is required because: for VerifyOTP we have it from request; for Refresh we fetch it.
func (u *Auth) openSession(ctx context.Context, acc entity.Account, email string, client inbound.ClientInfo, now time.Time, rotateFrom string) (dto.TokenResponse, error) {
	raw, err := u.d.Secrets.RefreshToken()
	if err != nil {
		return dto.TokenResponse{}, apperror.Internal(err)
	}
	next := entity.NewSession{AccountID: acc.ID, RefreshTokenHash: u.d.PII.HashRefreshToken(raw), UserAgent: truncate(client.UserAgent, 256), ExpiresAt: now.Add(u.s.RefreshTTL)}
	var sess entity.Session
	if rotateFrom == "" {
		sess, err = u.d.Sessions.Create(ctx, next)
	} else {
		sess, err = u.d.Sessions.Rotate(ctx, rotateFrom, next, now)
		if errors.Is(err, entity.ErrRevoked) {
			return dto.TokenResponse{}, inbound.ErrSessionInvalid
		}
	}
	if err != nil {
		return dto.TokenResponse{}, apperror.Internal(err)
	}

	// Fetch nikHash for token claims (nil if account has no NIK)
	nikHash, err := u.d.Accounts.GetNIKHashByID(ctx, acc.ID)
	if err != nil && !errors.Is(err, entity.ErrNotFound) {
		return dto.TokenResponse{}, apperror.Internal(fmt.Errorf("fetch nik hash: %w", err))
	}

	access, accessExp, err := u.d.Tokens.IssueAccess(acc.ID, email, nikHash, now)
	if err != nil {
		return dto.TokenResponse{}, apperror.Internal(err)
	}
	return mapper.ToTokenResponse(acc, access, accessExp, raw, sess.ExpiresAt), nil
}

func (u *Auth) audit(ctx context.Context, e entity.LoginEvent) {
	if err := u.d.Audit.Record(ctx, e); err != nil {
		u.d.Logger.Warn("gagal mencatat login_audit", "outcome", e.Outcome, "error", err)
	}
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n]
}
