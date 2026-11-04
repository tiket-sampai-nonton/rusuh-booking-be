package usecase

import (
	"bytes"
	"context"
	"errors"
	"io"
	"log/slog"
	"strconv"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	"github.com/tiket-sampai-nonton/rusuh-booking-be/pkg/apperror"
	"github.com/tiket-sampai-nonton/rusuh-booking-be/pkg/clock"
	"github.com/tiket-sampai-nonton/rusuh-booking-be/pkg/revocation"

	mock_outbound "github.com/tiket-sampai-nonton/rusuh-booking-be/services/user/_mock/user/port/outbound"
	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/user/internal/user/domain/dto"
	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/user/internal/user/domain/entity"
	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/user/internal/user/port/inbound"
	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/user/internal/user/port/outbound"
)

var (
	now      = time.Date(2026, 10, 19, 5, 0, 0, 0, time.UTC)
	client   = inbound.ClientInfo{IP: "203.0.113.9", UserAgent: "test"}
	emailH   = []byte{0xab, 0xcd}
	emailKey = "abcd"
	codeH    = []byte("code-hash")
	settings = AuthSettings{OTPTTL: 5 * time.Minute, ResendCooldown: time.Minute, MaxAttempts: 5,
		MaxPerEmailHour: 5, MaxPerIPHour: 20, RefreshTTL: 30 * 24 * time.Hour, AccessTTL: 15 * time.Minute,
		MaxVerifyPerIPMinute: 60, MaxSessionPerIPMinute: 300}
	activeAcc = entity.Account{ID: "acc-1", EmailMasked: "ar***@gmail.com", Status: entity.AccountActive}
)

type mocks struct {
	accounts  *mock_outbound.MockIAccountRepository
	sessions  *mock_outbound.MockISessionRepository
	audit     *mock_outbound.MockILoginAudit
	otp       *mock_outbound.MockIOTPStore
	limiter   *mock_outbound.MockIRateLimiter
	publisher *mock_outbound.MockIOTPPublisher
	tokens    *mock_outbound.MockITokenIssuer
	secrets   *mock_outbound.MockISecretGenerator
	pii       *mock_outbound.MockIPIIProtector
}

func newAuth(t *testing.T) (*Auth, mocks) {
	t.Helper()
	return newAuthWithLogger(t, slog.New(slog.NewTextHandler(io.Discard, nil)))
}

func newAuthWithLogger(t *testing.T, logger *slog.Logger) (*Auth, mocks) {
	t.Helper()
	ctrl := gomock.NewController(t)
	m := mocks{
		accounts:  mock_outbound.NewMockIAccountRepository(ctrl),
		sessions:  mock_outbound.NewMockISessionRepository(ctrl),
		audit:     mock_outbound.NewMockILoginAudit(ctrl),
		otp:       mock_outbound.NewMockIOTPStore(ctrl),
		limiter:   mock_outbound.NewMockIRateLimiter(ctrl),
		publisher: mock_outbound.NewMockIOTPPublisher(ctrl),
		tokens:    mock_outbound.NewMockITokenIssuer(ctrl),
		secrets:   mock_outbound.NewMockISecretGenerator(ctrl),
		pii:       mock_outbound.NewMockIPIIProtector(ctrl),
	}
	m.pii.EXPECT().HashEmail("arnu@gmail.com").Return(emailH).AnyTimes()
	m.pii.EXPECT().HashOTP(emailH, "123456").Return(codeH).AnyTimes()
	m.pii.EXPECT().HashOTP(emailH, "654321").Return([]byte("other")).AnyTimes()
	m.pii.EXPECT().HashRefreshToken(gomock.Any()).DoAndReturn(func(raw string) []byte { return []byte("h:" + raw) }).AnyTimes()
	u := NewAuth(AuthDeps{Accounts: m.accounts, Sessions: m.sessions, Audit: m.audit, OTP: m.otp, Limiter: m.limiter,
		Publisher: m.publisher, Tokens: m.tokens, Secrets: m.secrets, PII: m.pii, Clock: clock.Fixed{T: now},
		Logger: logger}, settings)
	return u, m
}

func code(err error) apperror.Code { return apperror.From(err).Code }

func ticket(bucket string) outbound.RateTicket {
	return outbound.RateTicket{Allowed: true, Bucket: bucket}
}
func denied(bucket string) outbound.RateTicket {
	return outbound.RateTicket{Allowed: false, Bucket: bucket}
}

var (
	emailTicket = ticket("rl:email")
	ipTicket    = ticket("rl:ip")
)

// expectOTPQuota covers RequestOTP up to a started challenge: no cooldown, both limiters allow.
func expectOTPQuota(m mocks) {
	m.otp.EXPECT().InCooldown(gomock.Any(), emailKey).Return(false, nil)
	m.limiter.EXPECT().Allow(gomock.Any(), "otp:email:"+emailKey, 5, time.Hour).Return(emailTicket, nil)
	m.limiter.EXPECT().Allow(gomock.Any(), "otp:ip:203.0.113.9", 20, time.Hour).Return(ipTicket, nil)
}

func expectVerifyLimit(m mocks) {
	m.limiter.EXPECT().Allow(gomock.Any(), "verify:ip:203.0.113.9", 60, time.Minute).Return(ticket("rl:verify"), nil)
}

func expectSessionLimit(m mocks) {
	m.limiter.EXPECT().Allow(gomock.Any(), "session:ip:203.0.113.9", 300, time.Minute).Return(ticket("rl:session"), nil)
}

func TestRequestOTP_Success(t *testing.T) {
	u, m := newAuth(t)
	expectOTPQuota(m)
	m.secrets.EXPECT().OTPCode().Return("123456", nil)
	m.otp.EXPECT().Start(gomock.Any(), emailKey, codeH, 5*time.Minute, time.Minute).Return(true, nil)
	m.pii.EXPECT().SealEmail("arnu@gmail.com").Return([]byte("email-enc"), nil)
	m.pii.EXPECT().SealOTP("123456").Return([]byte("code-enc"), nil)
	m.publisher.EXPECT().PublishOTP(gomock.Any(), []byte("email-enc"), []byte("code-enc"), gomock.Any(), gomock.Any(), now.Add(5*time.Minute)).Return(nil)

	m.audit.EXPECT().Record(gomock.Any(), entity.LoginEvent{EmailHash: emailH, Outcome: entity.AuditOTPSent, ClientIP: client.IP}).Return(nil)
	resp, err := u.RequestOTP(context.Background(), dto.RequestOTPRequest{Email: " Arnu@Gmail.com "}, client)
	require.NoError(t, err)
	assert.Equal(t, dto.RequestOTPResponse{ExpiresInSeconds: 300, ResendAfterSeconds: 60}, resp)
}

// P-08 + P-07: the response does not depend on the account, and a cooldown rejection never touches the quota.
// Strict mocks: Accounts, Limiter, Start have no expectations, so any call fails the test.
func TestRequestOTP_CooldownRejectsBeforeQuotaAndAccountLookup(t *testing.T) {
	u, m := newAuth(t)
	m.otp.EXPECT().InCooldown(gomock.Any(), emailKey).Return(true, nil)
	_, err := u.RequestOTP(context.Background(), dto.RequestOTPRequest{Email: "arnu@gmail.com"}, client)
	assert.Equal(t, inbound.CodeOTPResendTooSoon, code(err))
	assert.Equal(t, "60", apperror.From(err).Details["retryAfterSeconds"])
}

func TestRequestOTP_LimiterKeysUseIPBucketAndNormalisedIP(t *testing.T) {
	u, m := newAuth(t)
	m.otp.EXPECT().InCooldown(gomock.Any(), emailKey).Return(false, nil)
	m.limiter.EXPECT().Allow(gomock.Any(), "otp:email:"+emailKey, 5, time.Hour).Return(emailTicket, nil)
	m.limiter.EXPECT().Allow(gomock.Any(), "otp:ip:2001:db8:1:2::/64", 20, time.Hour).Return(ipTicket, nil)
	m.secrets.EXPECT().OTPCode().Return("123456", nil)
	m.otp.EXPECT().Start(gomock.Any(), emailKey, codeH, gomock.Any(), gomock.Any()).Return(true, nil)
	m.pii.EXPECT().SealEmail(gomock.Any()).Return([]byte("e"), nil)
	m.pii.EXPECT().SealOTP(gomock.Any()).Return([]byte("c"), nil)
	m.publisher.EXPECT().PublishOTP(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).Return(nil)
	m.audit.EXPECT().Record(gomock.Any(), entity.LoginEvent{EmailHash: emailH, Outcome: entity.AuditOTPSent, ClientIP: "2001:db8:1:2::1"}).Return(nil)
	_, err := u.RequestOTP(context.Background(), dto.RequestOTPRequest{Email: "arnu@gmail.com"}, inbound.ClientInfo{IP: "2001:DB8:1:2::1"})
	require.NoError(t, err)
}

func TestRequestOTP_Failures(t *testing.T) {
	ctx := context.Background()
	req := dto.RequestOTPRequest{Email: "arnu@gmail.com"}

	t.Run("invalid email", func(t *testing.T) {
		u, _ := newAuth(t)
		_, err := u.RequestOTP(ctx, dto.RequestOTPRequest{Email: "nope"}, client)
		assert.Equal(t, apperror.CodeValidationFailed, code(err))
		assert.Equal(t, "format email tidak valid", apperror.From(err).Details["email"])
	})
	t.Run("cooldown check error", func(t *testing.T) {
		u, m := newAuth(t)
		m.otp.EXPECT().InCooldown(gomock.Any(), emailKey).Return(false, errors.New("down"))
		_, err := u.RequestOTP(ctx, req, client)
		assert.Equal(t, apperror.CodeInternal, code(err))
	})
	t.Run("email rate limited keeps its own count and writes audit", func(t *testing.T) {
		u, m := newAuth(t)
		m.otp.EXPECT().InCooldown(gomock.Any(), emailKey).Return(false, nil)
		m.limiter.EXPECT().Allow(gomock.Any(), "otp:email:"+emailKey, 5, time.Hour).Return(denied("rl:email"), nil)
		m.audit.EXPECT().Record(gomock.Any(), entity.LoginEvent{EmailHash: emailH, Outcome: entity.AuditOTPRateLimited, ClientIP: client.IP}).Return(nil)
		_, err := u.RequestOTP(ctx, req, client)
		assert.Equal(t, apperror.CodeRateLimited, code(err))
	})
	t.Run("ip rate limited refunds the email unit", func(t *testing.T) {
		u, m := newAuth(t)
		m.otp.EXPECT().InCooldown(gomock.Any(), emailKey).Return(false, nil)
		m.limiter.EXPECT().Allow(gomock.Any(), "otp:email:"+emailKey, 5, time.Hour).Return(emailTicket, nil)
		m.limiter.EXPECT().Allow(gomock.Any(), "otp:ip:203.0.113.9", 20, time.Hour).Return(denied("rl:ip"), nil)
		m.limiter.EXPECT().Release(gomock.Any(), emailTicket).Return(nil)
		m.audit.EXPECT().Record(gomock.Any(), entity.LoginEvent{EmailHash: emailH, Outcome: entity.AuditOTPRateLimited, ClientIP: client.IP}).Return(nil)
		_, err := u.RequestOTP(ctx, req, client)
		assert.Equal(t, apperror.CodeRateLimited, code(err))
	})
	t.Run("email limiter error", func(t *testing.T) {
		u, m := newAuth(t)
		m.otp.EXPECT().InCooldown(gomock.Any(), emailKey).Return(false, nil)
		m.limiter.EXPECT().Allow(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).Return(outbound.RateTicket{}, errors.New("valkey down"))
		_, err := u.RequestOTP(ctx, req, client)
		assert.Equal(t, apperror.CodeInternal, code(err))
	})
	t.Run("ip limiter error refunds email", func(t *testing.T) {
		u, m := newAuth(t)
		m.otp.EXPECT().InCooldown(gomock.Any(), emailKey).Return(false, nil)
		m.limiter.EXPECT().Allow(gomock.Any(), "otp:email:"+emailKey, 5, time.Hour).Return(emailTicket, nil)
		m.limiter.EXPECT().Allow(gomock.Any(), "otp:ip:203.0.113.9", 20, time.Hour).Return(outbound.RateTicket{}, errors.New("valkey down"))
		m.limiter.EXPECT().Release(gomock.Any(), emailTicket).Return(nil)
		_, err := u.RequestOTP(ctx, req, client)
		assert.Equal(t, apperror.CodeInternal, code(err))
	})
	t.Run("generator error refunds both", func(t *testing.T) {
		u, m := newAuth(t)
		expectOTPQuota(m)
		m.secrets.EXPECT().OTPCode().Return("", errors.New("entropy"))
		m.limiter.EXPECT().Release(gomock.Any(), emailTicket).Return(nil)
		m.limiter.EXPECT().Release(gomock.Any(), ipTicket).Return(nil)
		_, err := u.RequestOTP(ctx, req, client)
		assert.Equal(t, apperror.CodeInternal, code(err))
	})
	t.Run("store error refunds both", func(t *testing.T) {
		u, m := newAuth(t)
		expectOTPQuota(m)
		m.secrets.EXPECT().OTPCode().Return("123456", nil)
		m.otp.EXPECT().Start(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).Return(false, errors.New("down"))
		m.limiter.EXPECT().Release(gomock.Any(), emailTicket).Return(nil)
		m.limiter.EXPECT().Release(gomock.Any(), ipTicket).Return(errors.New("refund failed is only logged"))
		_, err := u.RequestOTP(ctx, req, client)
		assert.Equal(t, apperror.CodeInternal, code(err))
	})
	t.Run("lost the SET NX race refunds both", func(t *testing.T) {
		u, m := newAuth(t)
		expectOTPQuota(m)
		m.secrets.EXPECT().OTPCode().Return("123456", nil)
		m.otp.EXPECT().Start(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).Return(false, nil)
		m.limiter.EXPECT().Release(gomock.Any(), emailTicket).Return(nil)
		m.limiter.EXPECT().Release(gomock.Any(), ipTicket).Return(nil)
		_, err := u.RequestOTP(ctx, req, client)
		assert.Equal(t, inbound.CodeOTPResendTooSoon, code(err))
		assert.Equal(t, "60", apperror.From(err).Details["retryAfterSeconds"])
	})
	t.Run("seal email error cancels the challenge", func(t *testing.T) {
		u, m := newAuth(t)
		expectOTPQuota(m)
		m.secrets.EXPECT().OTPCode().Return("123456", nil)
		m.otp.EXPECT().Start(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).Return(true, nil)
		m.pii.EXPECT().SealEmail(gomock.Any()).Return(nil, errors.New("aes"))
		m.otp.EXPECT().Cancel(gomock.Any(), emailKey).Return(nil)
		_, err := u.RequestOTP(ctx, req, client)
		assert.Equal(t, apperror.CodeInternal, code(err))
	})
	t.Run("seal otp error cancels the challenge", func(t *testing.T) {
		u, m := newAuth(t)
		expectOTPQuota(m)
		m.secrets.EXPECT().OTPCode().Return("123456", nil)
		m.otp.EXPECT().Start(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).Return(true, nil)
		m.pii.EXPECT().SealEmail(gomock.Any()).Return([]byte("e"), nil)
		m.pii.EXPECT().SealOTP(gomock.Any()).Return(nil, errors.New("aes"))
		m.otp.EXPECT().Cancel(gomock.Any(), emailKey).Return(nil)
		_, err := u.RequestOTP(ctx, req, client)
		assert.Equal(t, apperror.CodeInternal, code(err))
	})
	t.Run("publish fails cancels challenge and cooldown, no refund", func(t *testing.T) {
		u, m := newAuth(t)
		expectOTPQuota(m)
		m.secrets.EXPECT().OTPCode().Return("123456", nil)
		m.otp.EXPECT().Start(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).Return(true, nil)
		m.pii.EXPECT().SealEmail(gomock.Any()).Return([]byte("e-enc"), nil)
		m.pii.EXPECT().SealOTP(gomock.Any()).Return([]byte("c-enc"), nil)
		m.publisher.EXPECT().PublishOTP(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).Return(errors.New("broker down"))
		m.otp.EXPECT().Cancel(gomock.Any(), emailKey).Return(errors.New("also down"))
		_, err := u.RequestOTP(ctx, req, client)
		assert.Equal(t, apperror.CodeDependencyUnavailable, code(err))
	})
}

// expectSession sets up the happy path of openSession: tokens and session id are prepared BEFORE the commit.
func expectSession(m mocks, rotateFrom string, email string) {
	m.secrets.EXPECT().RefreshToken().Return("refresh-raw", nil)
	m.secrets.EXPECT().SessionID().Return("sid-1", nil)
	m.accounts.EXPECT().GetNIKHashByID(gomock.Any(), "acc-1").Return(nil, nil) // no NIK
	m.tokens.EXPECT().IssueAccess("acc-1", "sid-1", email, nil, gomock.Any(), now).Return("access-jwt", now.Add(15*time.Minute), nil)
	sess := entity.Session{ID: "sid-1", AccountID: "acc-1", ExpiresAt: now.Add(settings.RefreshTTL)}
	next := entity.NewSession{ID: "sid-1", AccountID: "acc-1", RefreshTokenHash: []byte("h:refresh-raw"), UserAgent: "test", ExpiresAt: now.Add(settings.RefreshTTL)}
	if rotateFrom == "" {
		m.sessions.EXPECT().Create(gomock.Any(), next).Return(sess, nil)
	} else {
		m.sessions.EXPECT().Rotate(gomock.Any(), rotateFrom, next, now).Return(sess, nil)
	}
}

func matched() entity.OTPAttempt { return entity.OTPAttempt{Verdict: entity.OTPMatched, Attempts: 1} }

func expectMatched(m mocks) {
	expectVerifyLimit(m)
	m.otp.EXPECT().Verify(gomock.Any(), emailKey, codeH, 5).Return(matched(), nil)
}

func TestVerifyOTP_ExistingAccount(t *testing.T) {
	u, m := newAuth(t)
	expectMatched(m)
	m.accounts.EXPECT().FindByEmailHash(gomock.Any(), emailH).Return(activeAcc, true, nil)
	m.accounts.EXPECT().TouchLogin(gomock.Any(), "acc-1", now).Return(nil)
	expectSession(m, "", "arnu@gmail.com")

	m.audit.EXPECT().Record(gomock.Any(), entity.LoginEvent{AccountID: "acc-1", EmailHash: emailH, Outcome: entity.AuditLoginOK, ClientIP: client.IP}).Return(nil)
	resp, err := u.VerifyOTP(context.Background(), dto.VerifyOTPRequest{Email: "arnu@gmail.com", Code: "123456"}, client)
	require.NoError(t, err)
	assert.Equal(t, "access-jwt", resp.AccessToken)
	assert.Equal(t, "refresh-raw", resp.RefreshToken)
	assert.Equal(t, "acc-1", resp.Account.AccountID)
}

func TestVerifyOTP_FirstLoginCreatesAccount(t *testing.T) {
	u, m := newAuth(t)
	expectMatched(m)
	m.accounts.EXPECT().FindByEmailHash(gomock.Any(), emailH).Return(entity.Account{}, false, nil)
	m.pii.EXPECT().SealEmail("arnu@gmail.com").Return([]byte("enc"), nil)
	m.accounts.EXPECT().Create(gomock.Any(), entity.NewAccount{EmailHash: emailH, EmailEnc: []byte("enc"), EmailMasked: "ar***@gmail.com"}).Return(activeAcc, nil)
	m.accounts.EXPECT().TouchLogin(gomock.Any(), "acc-1", now).Return(nil)
	expectSession(m, "", "arnu@gmail.com")

	m.audit.EXPECT().Record(gomock.Any(), entity.LoginEvent{AccountID: "acc-1", EmailHash: emailH, Outcome: entity.AuditLoginOK, ClientIP: client.IP}).Return(nil)
	_, err := u.VerifyOTP(context.Background(), dto.VerifyOTPRequest{Email: "arnu@gmail.com", Code: "123456"}, client)
	require.NoError(t, err)
}

// P-01 (usecase side) / TC-USR-14: a second VerifyOTP after success gets OTP_EXPIRED and no second session.
func TestVerifyOTP_SecondUseAfterSuccessIsExpired(t *testing.T) {
	u, m := newAuth(t)
	m.limiter.EXPECT().Allow(gomock.Any(), "verify:ip:203.0.113.9", 60, time.Minute).Return(ticket("rl:verify"), nil).Times(2)
	gomock.InOrder(
		m.otp.EXPECT().Verify(gomock.Any(), emailKey, codeH, 5).Return(matched(), nil),
		m.otp.EXPECT().Verify(gomock.Any(), emailKey, codeH, 5).Return(entity.OTPAttempt{Verdict: entity.OTPNotFound}, nil),
	)
	m.accounts.EXPECT().FindByEmailHash(gomock.Any(), emailH).Return(activeAcc, true, nil).Times(1)
	m.accounts.EXPECT().TouchLogin(gomock.Any(), "acc-1", now).Return(nil).Times(1)
	m.secrets.EXPECT().RefreshToken().Return("refresh-raw", nil).Times(1)
	m.secrets.EXPECT().SessionID().Return("sid-1", nil).Times(1)
	m.accounts.EXPECT().GetNIKHashByID(gomock.Any(), "acc-1").Return(nil, nil).Times(1)
	m.tokens.EXPECT().IssueAccess(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).Return("a", now, nil).Times(1)
	m.sessions.EXPECT().Create(gomock.Any(), gomock.Any()).Return(entity.Session{ID: "sid-1", ExpiresAt: now.Add(time.Hour)}, nil).Times(1)
	m.audit.EXPECT().Record(gomock.Any(), gomock.Any()).Return(nil).Times(1) // hanya LOGIN_OK; percobaan kedua tanpa baris audit

	req := dto.VerifyOTPRequest{Email: "arnu@gmail.com", Code: "123456"}
	_, err := u.VerifyOTP(context.Background(), req, client)
	require.NoError(t, err)
	_, err = u.VerifyOTP(context.Background(), req, client)
	assert.Equal(t, inbound.CodeOTPExpired, code(err))
}

// TC-USR-20 (verify side): over the per-IP limit nothing downstream is touched; limiter error fails closed.
func TestVerifyOTP_Limiter(t *testing.T) {
	ctx := context.Background()
	req := dto.VerifyOTPRequest{Email: "arnu@gmail.com", Code: "123456"}
	t.Run("over limit", func(t *testing.T) {
		u, m := newAuth(t)
		m.limiter.EXPECT().Allow(gomock.Any(), "verify:ip:203.0.113.9", 60, time.Minute).Return(denied("rl:verify"), nil)
		_, err := u.VerifyOTP(ctx, req, client) // OTP store, accounts, sessions: tanpa expectation = tidak boleh dipanggil
		assert.Equal(t, apperror.CodeRateLimited, code(err))
	})
	t.Run("limiter error fails closed", func(t *testing.T) {
		u, m := newAuth(t)
		m.limiter.EXPECT().Allow(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).Return(outbound.RateTicket{}, errors.New("valkey down"))
		_, err := u.VerifyOTP(ctx, req, client)
		assert.Equal(t, apperror.CodeInternal, code(err))
	})
	t.Run("ipv6 clients in one /64 share the bucket", func(t *testing.T) {
		u, m := newAuth(t)
		m.limiter.EXPECT().Allow(gomock.Any(), "verify:ip:2001:db8:1:2::/64", 60, time.Minute).Return(denied("x"), nil)
		_, err := u.VerifyOTP(ctx, req, inbound.ClientInfo{IP: "2001:db8:1:2:aaaa::9"})
		assert.Equal(t, apperror.CodeRateLimited, code(err))
	})
}

// HRD-008.4: unauthenticated requests for emails that never requested a code write nothing to the database.
func TestVerifyOTP_NotFoundWritesNoAuditRow(t *testing.T) {
	u, m := newAuth(t) // Audit mock tanpa expectation: satu panggilan Record gagalkan test
	m.limiter.EXPECT().Allow(gomock.Any(), "verify:ip:203.0.113.9", 60, time.Minute).Return(ticket("rl"), nil).Times(1000)
	m.pii.EXPECT().HashEmail(gomock.Any()).Return(emailH).AnyTimes()
	m.pii.EXPECT().HashOTP(gomock.Any(), gomock.Any()).Return(codeH).AnyTimes()
	m.otp.EXPECT().Verify(gomock.Any(), gomock.Any(), gomock.Any(), 5).Return(entity.OTPAttempt{Verdict: entity.OTPNotFound}, nil).Times(1000)
	for i := 0; i < 1000; i++ {
		_, err := u.VerifyOTP(context.Background(), dto.VerifyOTPRequest{Email: "user" + strconv.Itoa(i) + "@example.com", Code: "123456"}, client)
		require.Equal(t, inbound.CodeOTPExpired, code(err))
	}
}

func TestVerifyOTP_MismatchCountsDownThenLocks(t *testing.T) {
	u, m := newAuth(t)
	m.limiter.EXPECT().Allow(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).Return(ticket("rl"), nil).Times(6)
	m.audit.EXPECT().Record(gomock.Any(), gomock.Any()).Return(nil).Times(6)
	for n := 1; n <= 5; n++ {
		m.otp.EXPECT().Verify(gomock.Any(), emailKey, []byte("other"), 5).Return(entity.OTPAttempt{Verdict: entity.OTPMismatch, Attempts: n}, nil)
	}
	m.otp.EXPECT().Verify(gomock.Any(), emailKey, []byte("other"), 5).Return(entity.OTPAttempt{Verdict: entity.OTPLocked, Attempts: 6}, nil)
	for n := 1; n <= 5; n++ {
		_, err := u.VerifyOTP(context.Background(), dto.VerifyOTPRequest{Email: "arnu@gmail.com", Code: "654321"}, client)
		assert.Equal(t, inbound.CodeOTPInvalid, code(err))
		assert.Equal(t, strconv.Itoa(5-n), apperror.From(err).Details["attemptsLeft"])
	}
	_, err := u.VerifyOTP(context.Background(), dto.VerifyOTPRequest{Email: "arnu@gmail.com", Code: "654321"}, client)
	assert.Equal(t, inbound.CodeOTPLocked, code(err))
}

func TestVerifyOTP_SanitisesClientBeforeStoring(t *testing.T) {
	u, m := newAuth(t)
	m.limiter.EXPECT().Allow(gomock.Any(), "verify:ip:unknown", 60, time.Minute).Return(ticket("rl"), nil) // IP rusak -> bucket unknown
	m.otp.EXPECT().Verify(gomock.Any(), emailKey, codeH, 5).Return(matched(), nil)
	m.accounts.EXPECT().FindByEmailHash(gomock.Any(), emailH).Return(activeAcc, true, nil)
	m.accounts.EXPECT().TouchLogin(gomock.Any(), "acc-1", now).Return(nil)
	m.secrets.EXPECT().RefreshToken().Return("refresh-raw", nil)
	m.secrets.EXPECT().SessionID().Return("sid-1", nil)
	m.accounts.EXPECT().GetNIKHashByID(gomock.Any(), "acc-1").Return(nil, nil)
	m.tokens.EXPECT().IssueAccess(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).Return("a", now, nil)
	var stored entity.NewSession
	m.sessions.EXPECT().Create(gomock.Any(), gomock.Any()).DoAndReturn(func(_ context.Context, n entity.NewSession) (entity.Session, error) {
		stored = n
		return entity.Session{ID: "sid-1", ExpiresAt: now.Add(time.Hour)}, nil
	})
	m.audit.EXPECT().Record(gomock.Any(), entity.LoginEvent{AccountID: "acc-1", EmailHash: emailH, Outcome: entity.AuditLoginOK, ClientIP: ""}).Return(nil)
	ua := strings.Repeat("日", 300) + "\xff\x00"
	_, err := u.VerifyOTP(context.Background(), dto.VerifyOTPRequest{Email: "arnu@gmail.com", Code: "123456"}, inbound.ClientInfo{IP: "300.1.1.1", UserAgent: ua})
	require.NoError(t, err)
	assert.True(t, utf8.ValidString(stored.UserAgent))
	assert.Equal(t, 256, utf8.RuneCountInString(stored.UserAgent))
}

func TestVerifyOTP_Failures(t *testing.T) {
	ctx := context.Background()
	good := dto.VerifyOTPRequest{Email: "arnu@gmail.com", Code: "123456"}
	tests := []struct {
		name  string
		req   dto.VerifyOTPRequest
		setup func(m mocks)
		want  apperror.Code
	}{
		{"bad email", dto.VerifyOTPRequest{Email: "x", Code: "123456"}, func(mocks) {}, apperror.CodeValidationFailed},
		{"bad code", dto.VerifyOTPRequest{Email: "arnu@gmail.com", Code: "12"}, func(mocks) {}, apperror.CodeValidationFailed},
		{"store error", good, func(m mocks) {
			expectVerifyLimit(m)
			m.otp.EXPECT().Verify(gomock.Any(), emailKey, codeH, 5).Return(entity.OTPAttempt{}, errors.New("down"))
		}, apperror.CodeInternal},
		{"unknown verdict", good, func(m mocks) {
			expectVerifyLimit(m)
			m.otp.EXPECT().Verify(gomock.Any(), emailKey, codeH, 5).Return(entity.OTPAttempt{Verdict: 99}, nil)
		}, apperror.CodeInternal},
		{"expired", good, func(m mocks) {
			expectVerifyLimit(m)
			m.otp.EXPECT().Verify(gomock.Any(), emailKey, codeH, 5).Return(entity.OTPAttempt{Verdict: entity.OTPNotFound}, nil)
		}, inbound.CodeOTPExpired},
		{"locked", good, func(m mocks) {
			expectVerifyLimit(m)
			m.otp.EXPECT().Verify(gomock.Any(), emailKey, codeH, 5).Return(entity.OTPAttempt{Verdict: entity.OTPLocked, Attempts: 6}, nil)
			m.audit.EXPECT().Record(gomock.Any(), entity.LoginEvent{EmailHash: emailH, Outcome: entity.AuditOTPLocked, ClientIP: client.IP}).Return(nil)
		}, inbound.CodeOTPLocked},
		{"wrong code", dto.VerifyOTPRequest{Email: "arnu@gmail.com", Code: "654321"}, func(m mocks) {
			expectVerifyLimit(m)
			m.otp.EXPECT().Verify(gomock.Any(), emailKey, []byte("other"), 5).Return(entity.OTPAttempt{Verdict: entity.OTPMismatch, Attempts: 5}, nil)
			m.audit.EXPECT().Record(gomock.Any(), entity.LoginEvent{EmailHash: emailH, Outcome: entity.AuditOTPInvalid, ClientIP: client.IP}).Return(nil)
		}, inbound.CodeOTPInvalid},
		{"find error", good, func(m mocks) {
			expectMatched(m)
			m.accounts.EXPECT().FindByEmailHash(gomock.Any(), emailH).Return(entity.Account{}, false, errors.New("db"))
		}, apperror.CodeInternal},
		{"seal error", good, func(m mocks) {
			expectMatched(m)
			m.accounts.EXPECT().FindByEmailHash(gomock.Any(), emailH).Return(entity.Account{}, false, nil)
			m.pii.EXPECT().SealEmail(gomock.Any()).Return(nil, errors.New("aes"))
		}, apperror.CodeInternal},
		{"create error", good, func(m mocks) {
			expectMatched(m)
			m.accounts.EXPECT().FindByEmailHash(gomock.Any(), emailH).Return(entity.Account{}, false, nil)
			m.pii.EXPECT().SealEmail(gomock.Any()).Return([]byte("e"), nil)
			m.accounts.EXPECT().Create(gomock.Any(), gomock.Any()).Return(entity.Account{}, errors.New("db"))
		}, apperror.CodeInternal},
		{"disabled", good, func(m mocks) {
			expectMatched(m)
			m.accounts.EXPECT().FindByEmailHash(gomock.Any(), emailH).Return(entity.Account{ID: "acc-1", Status: entity.AccountDisabled}, true, nil)
		}, inbound.CodeAccountDisabled},
		{"touch error", good, func(m mocks) {
			expectMatched(m)
			m.accounts.EXPECT().FindByEmailHash(gomock.Any(), emailH).Return(activeAcc, true, nil)
			m.accounts.EXPECT().TouchLogin(gomock.Any(), gomock.Any(), gomock.Any()).Return(errors.New("db"))
		}, apperror.CodeInternal},
		// OQ-04 (diterima): kode sudah terpakai bila pembuatan sesi gagal setelah Matched; pengguna meminta kode baru.
		{"session failure after match is accepted (code stays consumed)", good, func(m mocks) {
			expectMatched(m)
			m.accounts.EXPECT().FindByEmailHash(gomock.Any(), emailH).Return(activeAcc, true, nil)
			m.accounts.EXPECT().TouchLogin(gomock.Any(), gomock.Any(), gomock.Any()).Return(nil)
			m.secrets.EXPECT().RefreshToken().Return("", errors.New("entropy"))
		}, apperror.CodeInternal},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			u, m := newAuth(t)
			tt.setup(m)
			_, err := u.VerifyOTP(ctx, tt.req, client)
			assert.Equal(t, tt.want, code(err))
		})
	}
}

// TC-USR-15 / P-04: failures that need no database write must leave the old refresh token usable.
func TestOpenSession_FailuresBeforeCommitLeaveNoSessionWrite(t *testing.T) {
	ctx := context.Background()
	email := "arnu@gmail.com"
	for _, rotateFrom := range []string{"", "s-1"} { // Create dan Rotate: tanpa expectation = tidak boleh dipanggil
		t.Run("refresh generator/"+rotateFrom, func(t *testing.T) {
			u, m := newAuth(t)
			m.secrets.EXPECT().RefreshToken().Return("", errors.New("entropy"))
			_, err := u.openSession(ctx, activeAcc, email, client, now, rotateFrom)
			assert.Equal(t, apperror.CodeInternal, code(err))
		})
		t.Run("session id generator/"+rotateFrom, func(t *testing.T) {
			u, m := newAuth(t)
			m.secrets.EXPECT().RefreshToken().Return("r", nil)
			m.secrets.EXPECT().SessionID().Return("", errors.New("entropy"))
			_, err := u.openSession(ctx, activeAcc, email, client, now, rotateFrom)
			assert.Equal(t, apperror.CodeInternal, code(err))
		})
		t.Run("get nik hash error/"+rotateFrom, func(t *testing.T) {
			u, m := newAuth(t)
			m.secrets.EXPECT().RefreshToken().Return("r", nil)
			m.secrets.EXPECT().SessionID().Return("sid-1", nil)
			m.accounts.EXPECT().GetNIKHashByID(gomock.Any(), "acc-1").Return(nil, errors.New("db"))
			_, err := u.openSession(ctx, activeAcc, email, client, now, rotateFrom)
			assert.Equal(t, apperror.CodeInternal, code(err))
		})
		t.Run("issue access error/"+rotateFrom, func(t *testing.T) {
			u, m := newAuth(t)
			m.secrets.EXPECT().RefreshToken().Return("r", nil)
			m.secrets.EXPECT().SessionID().Return("sid-1", nil)
			m.accounts.EXPECT().GetNIKHashByID(gomock.Any(), "acc-1").Return(nil, nil)
			m.tokens.EXPECT().IssueAccess(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).Return("", time.Time{}, errors.New("sign"))
			_, err := u.openSession(ctx, activeAcc, email, client, now, rotateFrom)
			assert.Equal(t, apperror.CodeInternal, code(err))
		})
	}
	t.Run("nik not found is tolerated", func(t *testing.T) {
		u, m := newAuth(t)
		m.secrets.EXPECT().RefreshToken().Return("r", nil)
		m.secrets.EXPECT().SessionID().Return("sid-1", nil)
		m.accounts.EXPECT().GetNIKHashByID(gomock.Any(), "acc-1").Return(nil, entity.ErrNotFound)
		m.tokens.EXPECT().IssueAccess(gomock.Any(), "sid-1", gomock.Any(), nil, gomock.Any(), now).Return("a", now, nil)
		m.sessions.EXPECT().Create(gomock.Any(), gomock.Any()).Return(entity.Session{ID: "sid-1"}, nil)
		_, err := u.openSession(ctx, activeAcc, email, client, now, "")
		assert.NoError(t, err)
	})
	t.Run("create error", func(t *testing.T) {
		u, m := newAuth(t)
		m.secrets.EXPECT().RefreshToken().Return("r", nil)
		m.secrets.EXPECT().SessionID().Return("sid-1", nil)
		m.accounts.EXPECT().GetNIKHashByID(gomock.Any(), "acc-1").Return(nil, nil)
		m.tokens.EXPECT().IssueAccess(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).Return("a", now, nil)
		m.sessions.EXPECT().Create(gomock.Any(), gomock.Any()).Return(entity.Session{}, errors.New("db"))
		_, err := u.openSession(ctx, activeAcc, email, client, now, "")
		assert.Equal(t, apperror.CodeInternal, code(err))
	})
	t.Run("rotate lost race has no side effects", func(t *testing.T) {
		u, m := newAuth(t) // RevokeAllForAccount tanpa expectation: kalah balapan bukan pencurian
		m.secrets.EXPECT().RefreshToken().Return("r", nil)
		m.secrets.EXPECT().SessionID().Return("sid-1", nil)
		m.accounts.EXPECT().GetNIKHashByID(gomock.Any(), "acc-1").Return(nil, nil)
		m.tokens.EXPECT().IssueAccess(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).Return("a", now, nil)
		m.sessions.EXPECT().Rotate(gomock.Any(), "s-1", gomock.Any(), now).Return(entity.Session{}, entity.ErrRevoked)
		_, err := u.openSession(ctx, activeAcc, email, client, now, "s-1")
		assert.Equal(t, inbound.CodeSessionInvalid, code(err))
	})
}

func expectRefreshPrelude(m mocks, active entity.Session) {
	expectSessionLimit(m)
	m.sessions.EXPECT().FindByTokenHash(gomock.Any(), []byte("h:old")).Return(active, true, nil)
	m.accounts.EXPECT().GetByID(gomock.Any(), "acc-1").Return(activeAcc, nil)
	m.accounts.EXPECT().GetEmailEncByID(gomock.Any(), "acc-1").Return([]byte("enc-email"), nil)
	m.pii.EXPECT().UnsealEmail([]byte("enc-email")).Return("arnu@gmail.com", nil)
}

func TestRefresh(t *testing.T) {
	ctx := context.Background()
	req := dto.RefreshRequest{RefreshToken: "old"}
	active := entity.Session{ID: "s-1", AccountID: "acc-1", ExpiresAt: now.Add(time.Hour)}

	t.Run("rotates", func(t *testing.T) {
		u, m := newAuth(t)
		expectRefreshPrelude(m, active)
		expectSession(m, "s-1", "arnu@gmail.com")
		m.audit.EXPECT().Record(gomock.Any(), entity.LoginEvent{AccountID: "acc-1", Outcome: entity.AuditRefreshOK, ClientIP: client.IP}).Return(nil)
		resp, err := u.Refresh(ctx, req, client)
		require.NoError(t, err)
		assert.Equal(t, "refresh-raw", resp.RefreshToken)
	})

	// TC-USR-15 / P-04: after a pre-commit failure the same token still works and nothing is revoked.
	t.Run("retry after pre-commit failure succeeds without revoking anything", func(t *testing.T) {
		u, m := newAuth(t)
		expectSessionLimit(m)
		expectSessionLimit(m)
		m.sessions.EXPECT().FindByTokenHash(gomock.Any(), []byte("h:old")).Return(active, true, nil).Times(2)
		m.accounts.EXPECT().GetByID(gomock.Any(), "acc-1").Return(activeAcc, nil).Times(2)
		m.accounts.EXPECT().GetEmailEncByID(gomock.Any(), "acc-1").Return([]byte("enc-email"), nil).Times(2)
		m.pii.EXPECT().UnsealEmail([]byte("enc-email")).Return("arnu@gmail.com", nil).Times(2)
		m.secrets.EXPECT().RefreshToken().Return("refresh-raw", nil).Times(2)
		m.secrets.EXPECT().SessionID().Return("sid-1", nil).Times(2)
		gomock.InOrder(
			m.accounts.EXPECT().GetNIKHashByID(gomock.Any(), "acc-1").Return(nil, errors.New("db blip")),
			m.accounts.EXPECT().GetNIKHashByID(gomock.Any(), "acc-1").Return(nil, nil),
		)
		m.tokens.EXPECT().IssueAccess("acc-1", "sid-1", "arnu@gmail.com", nil, gomock.Any(), now).Return("access-jwt", now.Add(time.Minute), nil).Times(1)
		m.sessions.EXPECT().Rotate(gomock.Any(), "s-1", gomock.Any(), now).Return(entity.Session{ID: "sid-1", ExpiresAt: now.Add(time.Hour)}, nil).Times(1)
		m.audit.EXPECT().Record(gomock.Any(), gomock.Any()).Return(nil).Times(1)

		_, err := u.Refresh(ctx, req, client)
		assert.Equal(t, apperror.CodeInternal, code(err))
		_, err = u.Refresh(ctx, req, client)
		require.NoError(t, err)
	})

	// TC-USR-19 / P-10
	t.Run("reuse revokes everything and denylists every returned sid", func(t *testing.T) {
		u, m := newAuth(t)
		deny := &fakeDenylist{}
		u.d.Denylist = deny
		revoked := active
		revoked.RevokedAt = &now
		expectSessionLimit(m)
		m.sessions.EXPECT().FindByTokenHash(gomock.Any(), gomock.Any()).Return(revoked, true, nil)
		m.sessions.EXPECT().RevokeAllForAccount(gomock.Any(), "acc-1", now, now.Add(-(15*time.Minute+revocation.Leeway))).Return([]string{"s-1", "s-2", "s-3"}, nil)
		m.audit.EXPECT().Record(gomock.Any(), entity.LoginEvent{AccountID: "acc-1", Outcome: entity.AuditRefreshReused, ClientIP: client.IP}).Return(nil)
		_, err := u.Refresh(ctx, req, client)
		assert.Equal(t, inbound.CodeSessionInvalid, code(err))
		assert.Equal(t, []string{"s-1", "s-2", "s-3"}, deny.sids)
		for _, ttl := range deny.ttls {
			assert.Equal(t, 15*time.Minute+revocation.Leeway, ttl)
		}
	})
	t.Run("reuse: denylist failure is reported, audit written once only after the retry completes", func(t *testing.T) {
		u, m := newAuth(t)
		deny := &fakeDenylist{failOn: map[string]int{"s-2": 1}}
		u.d.Denylist = deny
		revoked := active
		revoked.RevokedAt = &now
		expectSessionLimit(m)
		expectSessionLimit(m)
		m.sessions.EXPECT().FindByTokenHash(gomock.Any(), gomock.Any()).Return(revoked, true, nil).Times(2)
		m.sessions.EXPECT().RevokeAllForAccount(gomock.Any(), "acc-1", now, gomock.Any()).Return([]string{"s-1", "s-2"}, nil).Times(2) // id sama pada pengulangan
		m.audit.EXPECT().Record(gomock.Any(), entity.LoginEvent{AccountID: "acc-1", Outcome: entity.AuditRefreshReused, ClientIP: client.IP}).Return(nil).Times(1)
		_, err := u.Refresh(ctx, req, client)
		assert.Equal(t, apperror.CodeInternal, code(err))
		_, err = u.Refresh(ctx, req, client)
		assert.Equal(t, inbound.CodeSessionInvalid, code(err))
		assert.Contains(t, deny.sids, "s-2")
	})
	t.Run("reuse with nil denylist still revokes sessions", func(t *testing.T) {
		u, m := newAuth(t)
		revoked := active
		revoked.RevokedAt = &now
		expectSessionLimit(m)
		m.sessions.EXPECT().FindByTokenHash(gomock.Any(), gomock.Any()).Return(revoked, true, nil)
		m.sessions.EXPECT().RevokeAllForAccount(gomock.Any(), "acc-1", now, gomock.Any()).Return([]string{"s-1"}, nil)
		m.audit.EXPECT().Record(gomock.Any(), gomock.Any()).Return(nil)
		_, err := u.Refresh(ctx, req, client)
		assert.Equal(t, inbound.CodeSessionInvalid, code(err))
	})

	// TC-USR-20 / P-12
	t.Run("over limit returns 429 before any repository call", func(t *testing.T) {
		u, m := newAuth(t)
		m.limiter.EXPECT().Allow(gomock.Any(), "session:ip:203.0.113.9", 300, time.Minute).Return(denied("x"), nil)
		_, err := u.Refresh(ctx, req, client)
		assert.Equal(t, apperror.CodeRateLimited, code(err))
	})
	t.Run("limiter error fails open with a warning", func(t *testing.T) {
		var buf bytes.Buffer
		u, m := newAuthWithLogger(t, slog.New(slog.NewTextHandler(&buf, nil)))
		m.limiter.EXPECT().Allow(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).Return(outbound.RateTicket{}, errors.New("valkey down"))
		m.sessions.EXPECT().FindByTokenHash(gomock.Any(), gomock.Any()).Return(entity.Session{}, false, nil)
		_, err := u.Refresh(ctx, req, client)
		assert.Equal(t, inbound.CodeSessionInvalid, code(err))
		assert.Contains(t, buf.String(), "level=WARN")
	})

	tests := []struct {
		name  string
		req   dto.RefreshRequest
		setup func(m mocks)
		want  apperror.Code
	}{
		{"empty", dto.RefreshRequest{}, func(mocks) {}, inbound.CodeSessionInvalid},
		{"lookup error", req, func(m mocks) {
			expectSessionLimit(m)
			m.sessions.EXPECT().FindByTokenHash(gomock.Any(), gomock.Any()).Return(entity.Session{}, false, errors.New("db"))
		}, apperror.CodeInternal},
		{"unknown", req, func(m mocks) {
			expectSessionLimit(m)
			m.sessions.EXPECT().FindByTokenHash(gomock.Any(), gomock.Any()).Return(entity.Session{}, false, nil)
		}, inbound.CodeSessionInvalid},
		{"reuse revoke error", req, func(m mocks) {
			expectSessionLimit(m)
			r := active
			r.RevokedAt = &now
			m.sessions.EXPECT().FindByTokenHash(gomock.Any(), gomock.Any()).Return(r, true, nil)
			m.sessions.EXPECT().RevokeAllForAccount(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).Return(nil, errors.New("db"))
		}, apperror.CodeInternal},
		{"expired", req, func(m mocks) {
			expectSessionLimit(m)
			e := active
			e.ExpiresAt = now
			m.sessions.EXPECT().FindByTokenHash(gomock.Any(), gomock.Any()).Return(e, true, nil)
		}, inbound.CodeSessionInvalid},
		{"account gone", req, func(m mocks) {
			expectSessionLimit(m)
			m.sessions.EXPECT().FindByTokenHash(gomock.Any(), gomock.Any()).Return(active, true, nil)
			m.accounts.EXPECT().GetByID(gomock.Any(), "acc-1").Return(entity.Account{}, entity.ErrNotFound)
		}, inbound.CodeSessionInvalid},
		{"account error", req, func(m mocks) {
			expectSessionLimit(m)
			m.sessions.EXPECT().FindByTokenHash(gomock.Any(), gomock.Any()).Return(active, true, nil)
			m.accounts.EXPECT().GetByID(gomock.Any(), "acc-1").Return(entity.Account{}, errors.New("db"))
		}, apperror.CodeInternal},
		{"disabled", req, func(m mocks) {
			expectSessionLimit(m)
			m.sessions.EXPECT().FindByTokenHash(gomock.Any(), gomock.Any()).Return(active, true, nil)
			m.accounts.EXPECT().GetByID(gomock.Any(), "acc-1").Return(entity.Account{ID: "acc-1", Status: entity.AccountDisabled}, nil)
		}, inbound.CodeAccountDisabled},
		{"fetch email error", req, func(m mocks) {
			expectSessionLimit(m)
			m.sessions.EXPECT().FindByTokenHash(gomock.Any(), gomock.Any()).Return(active, true, nil)
			m.accounts.EXPECT().GetByID(gomock.Any(), "acc-1").Return(activeAcc, nil)
			m.accounts.EXPECT().GetEmailEncByID(gomock.Any(), "acc-1").Return(nil, errors.New("db"))
		}, apperror.CodeInternal},
		{"unseal email error", req, func(m mocks) {
			expectSessionLimit(m)
			m.sessions.EXPECT().FindByTokenHash(gomock.Any(), gomock.Any()).Return(active, true, nil)
			m.accounts.EXPECT().GetByID(gomock.Any(), "acc-1").Return(activeAcc, nil)
			m.accounts.EXPECT().GetEmailEncByID(gomock.Any(), "acc-1").Return([]byte("enc"), nil)
			m.pii.EXPECT().UnsealEmail(gomock.Any()).Return("", errors.New("decrypt"))
		}, apperror.CodeInternal},
		{"rotate race", req, func(m mocks) {
			expectSessionLimit(m)
			m.sessions.EXPECT().FindByTokenHash(gomock.Any(), gomock.Any()).Return(active, true, nil)
			m.accounts.EXPECT().GetByID(gomock.Any(), "acc-1").Return(activeAcc, nil)
			m.accounts.EXPECT().GetEmailEncByID(gomock.Any(), "acc-1").Return([]byte("enc"), nil)
			m.pii.EXPECT().UnsealEmail(gomock.Any()).Return("arnu@gmail.com", nil)
			m.secrets.EXPECT().RefreshToken().Return("r", nil)
			m.secrets.EXPECT().SessionID().Return("sid-1", nil)
			m.accounts.EXPECT().GetNIKHashByID(gomock.Any(), "acc-1").Return(nil, nil)
			m.tokens.EXPECT().IssueAccess(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).Return("a", now, nil)
			m.sessions.EXPECT().Rotate(gomock.Any(), "s-1", gomock.Any(), now).Return(entity.Session{}, entity.ErrRevoked)
		}, inbound.CodeSessionInvalid},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			u, m := newAuth(t)
			tt.setup(m)
			_, err := u.Refresh(ctx, tt.req, client)
			assert.Equal(t, tt.want, code(err))
		})
	}
}

func TestLogout(t *testing.T) {
	ctx := context.Background()
	req := dto.RefreshRequest{RefreshToken: "tok"}
	active := entity.Session{ID: "s-1", AccountID: "acc-1", ExpiresAt: now.Add(time.Hour)}
	full := 15*time.Minute + revocation.Leeway

	t.Run("empty token returns before the limiter", func(t *testing.T) {
		u, _ := newAuth(t)
		assert.NoError(t, u.Logout(ctx, dto.RefreshRequest{}, client))
	})
	t.Run("revokes", func(t *testing.T) {
		u, m := newAuth(t)
		expectSessionLimit(m)
		m.sessions.EXPECT().FindByTokenHash(gomock.Any(), []byte("h:tok")).Return(active, true, nil)
		m.sessions.EXPECT().Revoke(gomock.Any(), "s-1", now).Return(nil)
		m.audit.EXPECT().Record(gomock.Any(), entity.LoginEvent{AccountID: "acc-1", Outcome: entity.AuditLogout, ClientIP: client.IP}).Return(nil)
		assert.NoError(t, u.Logout(ctx, req, client))
	})
	t.Run("unknown token is idempotent and never touches the denylist", func(t *testing.T) {
		u, m := newAuth(t)
		deny := &fakeDenylist{}
		u.d.Denylist = deny
		expectSessionLimit(m)
		m.sessions.EXPECT().FindByTokenHash(gomock.Any(), gomock.Any()).Return(entity.Session{}, false, nil)
		assert.NoError(t, u.Logout(ctx, req, client))
		assert.Empty(t, deny.sids)
	})
	t.Run("lookup error", func(t *testing.T) {
		u, m := newAuth(t)
		expectSessionLimit(m)
		m.sessions.EXPECT().FindByTokenHash(gomock.Any(), gomock.Any()).Return(entity.Session{}, false, errors.New("db"))
		assert.Equal(t, apperror.CodeInternal, code(u.Logout(ctx, req, client)))
	})
	t.Run("revoke error", func(t *testing.T) {
		u, m := newAuth(t)
		expectSessionLimit(m)
		m.sessions.EXPECT().FindByTokenHash(gomock.Any(), gomock.Any()).Return(active, true, nil)
		m.sessions.EXPECT().Revoke(gomock.Any(), gomock.Any(), gomock.Any()).Return(errors.New("db"))
		assert.Equal(t, apperror.CodeInternal, code(u.Logout(ctx, req, client)))
	})
	t.Run("access token of the session is revoked for its remaining life", func(t *testing.T) {
		u, m := newAuth(t)
		deny := &fakeDenylist{}
		u.d.Denylist = deny
		expectSessionLimit(m)
		m.sessions.EXPECT().FindByTokenHash(gomock.Any(), []byte("h:tok")).Return(active, true, nil)
		m.sessions.EXPECT().Revoke(gomock.Any(), "s-1", now).Return(nil)
		m.audit.EXPECT().Record(gomock.Any(), gomock.Any()).Return(nil)
		assert.NoError(t, u.Logout(ctx, req, client))
		assert.Equal(t, []string{"s-1"}, deny.sids)
		assert.Equal(t, full, deny.ttls[0], "umur access token ditambah jeda; tidak lebih lama")
	})

	// TC-USR-16 / P-06
	t.Run("denylist failure is reported, retry on the revoked session rewrites the entry with the remaining TTL", func(t *testing.T) {
		u, m := newAuth(t)
		deny := &fakeDenylist{failOn: map[string]int{"s-1": 1}}
		u.d.Denylist = deny
		revokedAt := now.Add(-5 * time.Minute)
		revoked := active
		revoked.RevokedAt = &revokedAt
		expectSessionLimit(m)
		expectSessionLimit(m)
		gomock.InOrder(
			m.sessions.EXPECT().FindByTokenHash(gomock.Any(), gomock.Any()).Return(active, true, nil),
			m.sessions.EXPECT().FindByTokenHash(gomock.Any(), gomock.Any()).Return(revoked, true, nil),
		)
		m.sessions.EXPECT().Revoke(gomock.Any(), "s-1", now).Return(nil).Times(1)
		// audit logout hanya pada transisi (percobaan pertama gagal sebelum audit, pengulangan tidak lagi transisi)
		assert.Equal(t, apperror.CodeInternal, code(u.Logout(ctx, req, client)))
		require.NoError(t, u.Logout(ctx, req, client))
		require.Len(t, deny.ttls, 2)
		assert.Equal(t, full-5*time.Minute, deny.ttls[1], "sisa TTL dihitung dari revoked_at")
		assert.LessOrEqual(t, deny.ttls[1], full)
	})
	t.Run("logging out twice with a working denylist succeeds both times", func(t *testing.T) {
		u, m := newAuth(t)
		deny := &fakeDenylist{}
		u.d.Denylist = deny
		revoked := active
		revoked.RevokedAt = &now
		expectSessionLimit(m)
		expectSessionLimit(m)
		gomock.InOrder(
			m.sessions.EXPECT().FindByTokenHash(gomock.Any(), gomock.Any()).Return(active, true, nil),
			m.sessions.EXPECT().FindByTokenHash(gomock.Any(), gomock.Any()).Return(revoked, true, nil),
		)
		m.sessions.EXPECT().Revoke(gomock.Any(), "s-1", now).Return(nil).Times(1)
		m.audit.EXPECT().Record(gomock.Any(), gomock.Any()).Return(nil).Times(1)
		require.NoError(t, u.Logout(ctx, req, client))
		require.NoError(t, u.Logout(ctx, req, client))
	})
	t.Run("session revoked long ago needs no denylist entry", func(t *testing.T) {
		u, m := newAuth(t)
		deny := &fakeDenylist{}
		u.d.Denylist = deny
		old := now.Add(-time.Hour)
		revoked := active
		revoked.RevokedAt = &old
		expectSessionLimit(m)
		m.sessions.EXPECT().FindByTokenHash(gomock.Any(), gomock.Any()).Return(revoked, true, nil)
		require.NoError(t, u.Logout(ctx, req, client))
		assert.Empty(t, deny.sids)
	})
	t.Run("over limit", func(t *testing.T) {
		u, m := newAuth(t)
		m.limiter.EXPECT().Allow(gomock.Any(), "session:ip:203.0.113.9", 300, time.Minute).Return(denied("x"), nil)
		assert.Equal(t, apperror.CodeRateLimited, code(u.Logout(ctx, req, client)))
	})
	t.Run("limiter error fails open", func(t *testing.T) {
		u, m := newAuth(t)
		m.limiter.EXPECT().Allow(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).Return(outbound.RateTicket{}, errors.New("valkey down"))
		m.sessions.EXPECT().FindByTokenHash(gomock.Any(), gomock.Any()).Return(entity.Session{}, false, nil)
		assert.NoError(t, u.Logout(ctx, req, client))
	})
}

type fakeDenylist struct {
	sids   []string
	ttls   []time.Duration
	failOn map[string]int // sid -> jumlah kegagalan sebelum berhasil
	err    error
}

func (f *fakeDenylist) Revoke(_ context.Context, sid string, ttl time.Duration) error {
	f.sids = append(f.sids, sid)
	f.ttls = append(f.ttls, ttl)
	if n := f.failOn[sid]; n > 0 {
		f.failOn[sid] = n - 1
		return errors.New("valkey")
	}
	return f.err
}
func (f *fakeDenylist) IsRevoked(context.Context, string) (bool, error) { return false, nil }

func TestAuditFailureDoesNotBreakFlow(t *testing.T) {
	ctrl := gomock.NewController(t)
	audit := mock_outbound.NewMockILoginAudit(ctrl)
	audit.EXPECT().Record(gomock.Any(), gomock.Any()).Return(errors.New("db"))
	u := NewAuth(AuthDeps{Audit: audit, Logger: slog.New(slog.NewTextHandler(io.Discard, nil))}, settings)
	u.audit(context.Background(), entity.LoginEvent{Outcome: entity.AuditLogout})
}
