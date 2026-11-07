package usecase

import (
	"context"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"strconv"
	"time"

	"github.com/tiket-sampai-nonton/rusuh-booking-be/pkg/apperror"
	"github.com/tiket-sampai-nonton/rusuh-booking-be/pkg/clock"
	"github.com/tiket-sampai-nonton/rusuh-booking-be/pkg/revocation"

	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/user/internal/user/domain/dto"
	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/user/internal/user/domain/entity"
	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/user/internal/user/domain/mapper"
	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/user/internal/user/domain/valueobject"
	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/user/internal/user/port/inbound"
	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/user/internal/user/port/outbound"
)

// AuthSettings are the OTP and session limits (dari config OTP_* dan TOKEN_*).
type AuthSettings struct {
	OTPTTL          time.Duration
	ResendCooldown  time.Duration
	MaxAttempts     int
	MaxPerEmailHour int
	MaxPerIPHour    int
	RefreshTTL      time.Duration
	AccessTTL       time.Duration // umur access token; menentukan berapa lama sid logout disimpan di denylist
	// MaxVerifyPerIPMinute membatasi VerifyOTP per IP per menit (gagal tertutup bila limiter error).
	MaxVerifyPerIPMinute int
	// MaxSessionPerIPMinute membatasi Refresh dan Logout per IP per menit (gagal terbuka bila limiter error).
	MaxSessionPerIPMinute int
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
	Denylist  revocation.Denylist // nil = access token tidak dicabut saat logout (hanya sesi refresh)
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
	client = normalizeClient(client)
	emailHash := u.d.PII.HashEmail(email.String())
	key := hex.EncodeToString(emailHash)

	// Cooldown dicek lebih dulu: permintaan yang pasti ditolak tidak boleh membakar kuota per jam.
	inCooldown, err := u.d.OTP.InCooldown(ctx, key)
	if err != nil {
		return dto.RequestOTPResponse{}, apperror.Internal(fmt.Errorf("cek cooldown otp: %w", err))
	}
	if inCooldown {
		return dto.RequestOTPResponse{}, u.resendTooSoon()
	}

	emailTicket, err := u.d.Limiter.Allow(ctx, "otp:email:"+key, u.s.MaxPerEmailHour, time.Hour)
	if err != nil {
		return dto.RequestOTPResponse{}, apperror.Internal(fmt.Errorf("rate limit: %w", err))
	}
	if !emailTicket.Allowed {
		u.audit(ctx, entity.LoginEvent{EmailHash: emailHash, Outcome: entity.AuditOTPRateLimited, ClientIP: client.IP})
		return dto.RequestOTPResponse{}, inbound.ErrRateLimited
	}
	ipTicket, err := u.d.Limiter.Allow(ctx, "otp:ip:"+ipBucket(client.IP), u.s.MaxPerIPHour, time.Hour)
	if err != nil {
		u.release(ctx, emailTicket)
		return dto.RequestOTPResponse{}, apperror.Internal(fmt.Errorf("rate limit: %w", err))
	}
	if !ipTicket.Allowed {
		u.release(ctx, emailTicket) // limiter yang menolak menyimpan hitungannya sendiri; yang lolos dikembalikan
		u.audit(ctx, entity.LoginEvent{EmailHash: emailHash, Outcome: entity.AuditOTPRateLimited, ClientIP: client.IP})
		return dto.RequestOTPResponse{}, inbound.ErrRateLimited
	}

	code, err := u.d.Secrets.OTPCode()
	if err != nil {
		u.release(ctx, emailTicket, ipTicket)
		return dto.RequestOTPResponse{}, apperror.Internal(err)
	}
	started, err := u.d.OTP.Start(ctx, key, u.d.PII.HashOTP(emailHash, code), u.s.OTPTTL, u.s.ResendCooldown)
	if err != nil {
		u.release(ctx, emailTicket, ipTicket)
		return dto.RequestOTPResponse{}, apperror.Internal(fmt.Errorf("simpan otp: %w", err))
	}
	if !started {
		// Kalah balapan SET NX: satu-satunya penentu tetap cooldown di store.
		u.release(ctx, emailTicket, ipTicket)
		return dto.RequestOTPResponse{}, u.resendTooSoon()
	}
	// Kegagalan setelah ini membatalkan challenge DAN cooldown (kode tidak sampai). Kuota tidak dikembalikan:
	// publish yang ack-nya tidak terkonfirmasi mungkin sudah terkirim.
	// Enkripsi email dan kode sebelum dipublish — OTP adalah secret, tidak boleh plaintext di broker (SRS-F-27-12).
	emailEnc, err := u.d.PII.SealEmail(email.String())
	if err != nil {
		u.cancel(ctx, key)
		return dto.RequestOTPResponse{}, apperror.Internal(fmt.Errorf("seal email: %w", err))
	}
	codeEnc, err := u.d.PII.SealOTP(code)
	if err != nil {
		u.cancel(ctx, key)
		return dto.RequestOTPResponse{}, apperror.Internal(fmt.Errorf("seal otp: %w", err))
	}
	expiresAt := u.d.Clock.Now().Add(u.s.OTPTTL)
	if err := u.d.Publisher.PublishOTP(ctx, emailEnc, codeEnc, emailHash, u.d.Clock.Now(), expiresAt); err != nil {
		u.cancel(ctx, key)
		u.d.Logger.Error("publish otp.requested gagal", "error", err)
		return dto.RequestOTPResponse{}, inbound.ErrPublishFailed
	}
	u.audit(ctx, entity.LoginEvent{EmailHash: emailHash, Outcome: entity.AuditOTPSent, ClientIP: client.IP})
	return dto.RequestOTPResponse{ExpiresInSeconds: int(u.s.OTPTTL.Seconds()), ResendAfterSeconds: int(u.s.ResendCooldown.Seconds())}, nil
}

func (u *Auth) resendTooSoon() error {
	return inbound.ErrOTPResendTooSoon.WithDetails(map[string]string{
		"retryAfterSeconds": strconv.Itoa(int(u.s.ResendCooldown.Seconds()))})
}

// release gives back quota units best effort; kegagalan hanya membuat limiter lebih ketat, jadi cukup di-log.
func (u *Auth) release(ctx context.Context, tickets ...outbound.RateTicket) {
	for _, t := range tickets {
		if err := u.d.Limiter.Release(ctx, t); err != nil {
			u.d.Logger.Warn("gagal mengembalikan kuota rate limit", "error", err)
		}
	}
}

func (u *Auth) cancel(ctx context.Context, key string) {
	if err := u.d.OTP.Cancel(ctx, key); err != nil {
		u.d.Logger.Warn("gagal membatalkan otp", "error", err)
	}
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
	client = normalizeClient(client)

	// Batas per IP sebelum menyentuh store atau database. Gagal tertutup: tanpa limiter, brute force tak terbatas.
	ticket, err := u.d.Limiter.Allow(ctx, "verify:ip:"+ipBucket(client.IP), u.s.MaxVerifyPerIPMinute, time.Minute)
	if err != nil {
		return dto.TokenResponse{}, apperror.Internal(fmt.Errorf("rate limit: %w", err))
	}
	if !ticket.Allowed {
		return dto.TokenResponse{}, inbound.ErrRateLimited
	}

	emailHash := u.d.PII.HashEmail(email.String())
	key := hex.EncodeToString(emailHash)

	res, err := u.d.OTP.Verify(ctx, key, u.d.PII.HashOTP(emailHash, code.String()), u.s.MaxAttempts)
	if err != nil {
		return dto.TokenResponse{}, apperror.Internal(fmt.Errorf("verifikasi otp: %w", err))
	}
	switch res.Verdict {
	case entity.OTPMatched:
	case entity.OTPNotFound:
		// Tanpa baris audit: banjir permintaan untuk email acak tidak boleh menulis ke database.
		return dto.TokenResponse{}, inbound.ErrOTPExpired
	case entity.OTPLocked:
		u.audit(ctx, entity.LoginEvent{EmailHash: emailHash, Outcome: entity.AuditOTPLocked, ClientIP: client.IP})
		return dto.TokenResponse{}, inbound.ErrOTPLocked
	case entity.OTPMismatch:
		u.audit(ctx, entity.LoginEvent{EmailHash: emailHash, Outcome: entity.AuditOTPInvalid, ClientIP: client.IP})
		left := u.s.MaxAttempts - res.Attempts
		if left < 0 {
			left = 0
		}
		return dto.TokenResponse{}, inbound.ErrOTPInvalid.WithDetails(map[string]string{"attemptsLeft": strconv.Itoa(left)})
	default:
		return dto.TokenResponse{}, apperror.Internal(errors.New("verdict otp tak dikenal"))
	}

	// Kode sudah dikonsumsi secara atomik. Kegagalan di bawah ini mengharuskan pengguna meminta kode baru (OQ-04):
	// mengembalikan kode akan membuka lagi jendela replay yang baru saja ditutup.
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

// sessionLimit applies the per-IP limit of Refresh and Logout. Gagal terbuka: gangguan Valkey tidak boleh
// mengeluarkan semua pengguna (best-effort-vs-required).
func (u *Auth) sessionLimit(ctx context.Context, client inbound.ClientInfo) error {
	ticket, err := u.d.Limiter.Allow(ctx, "session:ip:"+ipBucket(client.IP), u.s.MaxSessionPerIPMinute, time.Minute)
	if err != nil {
		u.d.Logger.Warn("rate limit sesi gagal; dilewati", "error", err)
		return nil
	}
	if !ticket.Allowed {
		return inbound.ErrRateLimited
	}
	return nil
}

// Refresh rotates the refresh token. Token lama yang dipakai ulang mencabut semua sesi akun (deteksi pencurian).
func (u *Auth) Refresh(ctx context.Context, req dto.RefreshRequest, client inbound.ClientInfo) (dto.TokenResponse, error) {
	if req.RefreshToken == "" {
		return dto.TokenResponse{}, inbound.ErrSessionInvalid
	}
	client = normalizeClient(client)
	if err := u.sessionLimit(ctx, client); err != nil {
		return dto.TokenResponse{}, err
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
		if err := u.handleReuse(ctx, sess.AccountID, now, client); err != nil {
			return dto.TokenResponse{}, err
		}
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

// handleReuse revokes every session of the account AND denylists their live access tokens. Audit hanya ditulis
// setelah semua penulisan denylist berhasil; bila ada yang gagal, klien mengulang dan id yang sama dikembalikan
// (jendela AccessTTL+Leeway), jadi hasilnya konvergen.
func (u *Auth) handleReuse(ctx context.Context, accountID string, now time.Time, client inbound.ClientInfo) error {
	window := u.s.AccessTTL + revocation.Leeway
	ids, err := u.d.Sessions.RevokeAllForAccount(ctx, accountID, now, now.Add(-window))
	if err != nil {
		return apperror.Internal(err)
	}
	if u.d.Denylist != nil {
		for _, id := range ids {
			if err := u.d.Denylist.Revoke(ctx, id, window); err != nil {
				return apperror.Internal(fmt.Errorf("denylist reuse: %w", err))
			}
		}
	}
	u.audit(ctx, entity.LoginEvent{AccountID: accountID, Outcome: entity.AuditRefreshReused, ClientIP: client.IP})
	return nil
}

// Logout revokes the session. Idempoten: token tidak dikenal tetap dijawab sukses, dan pengulangan setelah
// penulisan denylist gagal menulis ulang entri denylist dengan sisa TTL.
func (u *Auth) Logout(ctx context.Context, req dto.RefreshRequest, client inbound.ClientInfo) error {
	if req.RefreshToken == "" {
		return nil
	}
	client = normalizeClient(client)
	if err := u.sessionLimit(ctx, client); err != nil {
		return err
	}
	sess, found, err := u.d.Sessions.FindByTokenHash(ctx, u.d.PII.HashRefreshToken(req.RefreshToken))
	if err != nil {
		return apperror.Internal(err)
	}
	if !found {
		return nil
	}
	now := u.d.Clock.Now()
	revokedAt := now
	transitioned := false
	if sess.Revoked() {
		revokedAt = *sess.RevokedAt
	} else {
		if err := u.d.Sessions.Revoke(ctx, sess.ID, now); err != nil {
			return apperror.Internal(err)
		}
		transitioned = true
	}
	// Access token sesi ini berhenti berlaku sekarang, bukan baru saat exp. Bila denylist gagal ditulis, logout dijawab
	// error supaya klien mengulang; sisa TTL dihitung dari revoked_at sehingga entri tidak pernah hidup lebih lama
	// dari access token terpanjang.
	if u.d.Denylist != nil {
		remaining := u.s.AccessTTL + revocation.Leeway - now.Sub(revokedAt)
		if remaining > u.s.AccessTTL+revocation.Leeway {
			remaining = u.s.AccessTTL + revocation.Leeway // jam miring: jangan melebihi umur access token terpanjang
		}
		if remaining > 0 {
			if err := u.d.Denylist.Revoke(ctx, sess.ID, remaining); err != nil {
				return apperror.Internal(err)
			}
		}
	}
	if transitioned {
		u.audit(ctx, entity.LoginEvent{AccountID: sess.AccountID, Outcome: entity.AuditLogout, ClientIP: client.IP})
	}
	return nil
}

// openSession issues a refresh token (baru, atau rotasi dari rotateFrom) and an access token.
// Semua yang bisa gagal tanpa efek samping (token, claim, id sesi) disiapkan SEBELUM commit sesi, sehingga
// kegagalan setelahnya tidak menghanguskan refresh token lama (HRD-003). email dibutuhkan untuk klaim token.
func (u *Auth) openSession(ctx context.Context, acc entity.Account, email string, client inbound.ClientInfo, now time.Time, rotateFrom string) (dto.TokenResponse, error) {
	raw, err := u.d.Secrets.RefreshToken()
	if err != nil {
		return dto.TokenResponse{}, apperror.Internal(err)
	}
	sid, err := u.d.Secrets.SessionID()
	if err != nil {
		return dto.TokenResponse{}, apperror.Internal(err)
	}
	// Fetch nikHash for token claims (nil if account has no NIK)
	nikHash, err := u.d.Accounts.GetNIKHashByID(ctx, acc.ID)
	if err != nil && !errors.Is(err, entity.ErrNotFound) {
		return dto.TokenResponse{}, apperror.Internal(fmt.Errorf("fetch nik hash: %w", err))
	}
	access, accessExp, err := u.d.Tokens.IssueAccess(acc.ID, sid, email, nikHash, acc.NIKMasked, now)
	if err != nil {
		return dto.TokenResponse{}, apperror.Internal(err)
	}

	next := entity.NewSession{ID: sid, AccountID: acc.ID, RefreshTokenHash: u.d.PII.HashRefreshToken(raw), UserAgent: client.UserAgent, ExpiresAt: now.Add(u.s.RefreshTTL)}
	var sess entity.Session
	if rotateFrom == "" {
		sess, err = u.d.Sessions.Create(ctx, next)
	} else {
		sess, err = u.d.Sessions.Rotate(ctx, rotateFrom, next, now)
		if errors.Is(err, entity.ErrRevoked) {
			// Access token yang belum dikembalikan dibuang; sid-nya tidak pernah ada sebagai baris sesi.
			return dto.TokenResponse{}, inbound.ErrSessionInvalid
		}
	}
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
