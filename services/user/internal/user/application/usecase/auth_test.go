package usecase

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	"github.com/tiket-sampai-nonton/rusuh-booking-be/pkg/apperror"
	"github.com/tiket-sampai-nonton/rusuh-booking-be/pkg/clock"

	mock_outbound "github.com/tiket-sampai-nonton/rusuh-booking-be/services/user/_mock/user/port/outbound"
	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/user/internal/user/domain/dto"
	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/user/internal/user/domain/entity"
	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/user/internal/user/port/inbound"
)

var (
	now      = time.Date(2026, 10, 19, 5, 0, 0, 0, time.UTC)
	client   = inbound.ClientInfo{IP: "203.0.113.9", UserAgent: "test"}
	emailH   = []byte{0xab, 0xcd}
	emailKey = "abcd"
	codeH    = []byte("code-hash")
	settings = AuthSettings{OTPTTL: 5 * time.Minute, ResendCooldown: time.Minute, MaxAttempts: 5,
		MaxPerEmailHour: 5, MaxPerIPHour: 20, RefreshTTL: 30 * 24 * time.Hour}
	activeAcc = entity.Account{ID: "acc-1", EmailMasked: "ar***@gmail.com", Status: entity.AccountActive}
)

type mocks struct {
	accounts *mock_outbound.MockIAccountRepository
	sessions *mock_outbound.MockISessionRepository
	audit    *mock_outbound.MockILoginAudit
	otp      *mock_outbound.MockIOTPStore
	limiter  *mock_outbound.MockIRateLimiter
	mailer   *mock_outbound.MockIMailer
	tokens   *mock_outbound.MockITokenIssuer
	secrets  *mock_outbound.MockISecretGenerator
	pii      *mock_outbound.MockIPIIProtector
}

func newAuth(t *testing.T) (*Auth, mocks) {
	t.Helper()
	ctrl := gomock.NewController(t)
	m := mocks{
		accounts: mock_outbound.NewMockIAccountRepository(ctrl),
		sessions: mock_outbound.NewMockISessionRepository(ctrl),
		audit:    mock_outbound.NewMockILoginAudit(ctrl),
		otp:      mock_outbound.NewMockIOTPStore(ctrl),
		limiter:  mock_outbound.NewMockIRateLimiter(ctrl),
		mailer:   mock_outbound.NewMockIMailer(ctrl),
		tokens:   mock_outbound.NewMockITokenIssuer(ctrl),
		secrets:  mock_outbound.NewMockISecretGenerator(ctrl),
		pii:      mock_outbound.NewMockIPIIProtector(ctrl),
	}
	m.pii.EXPECT().HashEmail("arnu@gmail.com").Return(emailH).AnyTimes()
	m.pii.EXPECT().HashOTP(emailH, "123456").Return(codeH).AnyTimes()
	m.pii.EXPECT().HashOTP(emailH, "654321").Return([]byte("other")).AnyTimes()
	m.pii.EXPECT().HashRefreshToken(gomock.Any()).DoAndReturn(func(raw string) []byte { return []byte("h:" + raw) }).AnyTimes()
	m.audit.EXPECT().Record(gomock.Any(), gomock.Any()).Return(nil).AnyTimes()
	u := NewAuth(AuthDeps{Accounts: m.accounts, Sessions: m.sessions, Audit: m.audit, OTP: m.otp, Limiter: m.limiter,
		Mailer: m.mailer, Tokens: m.tokens, Secrets: m.secrets, PII: m.pii, Clock: clock.Fixed{T: now},
		Logger: slog.New(slog.NewTextHandler(io.Discard, nil))}, settings)
	return u, m
}

func code(err error) apperror.Code { return apperror.From(err).Code }

func allowAll(m mocks) {
	m.limiter.EXPECT().Allow(gomock.Any(), "otp:email:"+emailKey, 5, time.Hour).Return(true, nil)
	m.limiter.EXPECT().Allow(gomock.Any(), "otp:ip:203.0.113.9", 20, time.Hour).Return(true, nil)
}

func TestRequestOTP_Success(t *testing.T) {
	u, m := newAuth(t)
	allowAll(m)
	m.secrets.EXPECT().OTPCode().Return("123456", nil)
	m.otp.EXPECT().Start(gomock.Any(), emailKey, codeH, 5*time.Minute, time.Minute).Return(true, nil)
	m.mailer.EXPECT().SendOTP(gomock.Any(), "arnu@gmail.com", "123456", 5*time.Minute).Return(nil)

	resp, err := u.RequestOTP(context.Background(), dto.RequestOTPRequest{Email: " Arnu@Gmail.com "}, client)
	require.NoError(t, err)
	assert.Equal(t, dto.RequestOTPResponse{ExpiresInSeconds: 300, ResendAfterSeconds: 60}, resp)
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
	t.Run("email rate limited", func(t *testing.T) {
		u, m := newAuth(t)
		m.limiter.EXPECT().Allow(gomock.Any(), "otp:email:"+emailKey, 5, time.Hour).Return(false, nil)
		_, err := u.RequestOTP(ctx, req, client)
		assert.Equal(t, apperror.CodeRateLimited, code(err))
	})
	t.Run("ip rate limited", func(t *testing.T) {
		u, m := newAuth(t)
		m.limiter.EXPECT().Allow(gomock.Any(), "otp:email:"+emailKey, 5, time.Hour).Return(true, nil)
		m.limiter.EXPECT().Allow(gomock.Any(), "otp:ip:203.0.113.9", 20, time.Hour).Return(false, nil)
		_, err := u.RequestOTP(ctx, req, client)
		assert.Equal(t, apperror.CodeRateLimited, code(err))
	})
	t.Run("limiter error", func(t *testing.T) {
		u, m := newAuth(t)
		m.limiter.EXPECT().Allow(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).Return(false, errors.New("valkey down"))
		_, err := u.RequestOTP(ctx, req, client)
		assert.Equal(t, apperror.CodeInternal, code(err))
	})
	t.Run("generator error", func(t *testing.T) {
		u, m := newAuth(t)
		allowAll(m)
		m.secrets.EXPECT().OTPCode().Return("", errors.New("entropy"))
		_, err := u.RequestOTP(ctx, req, client)
		assert.Equal(t, apperror.CodeInternal, code(err))
	})
	t.Run("store error", func(t *testing.T) {
		u, m := newAuth(t)
		allowAll(m)
		m.secrets.EXPECT().OTPCode().Return("123456", nil)
		m.otp.EXPECT().Start(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).Return(false, errors.New("down"))
		_, err := u.RequestOTP(ctx, req, client)
		assert.Equal(t, apperror.CodeInternal, code(err))
	})
	t.Run("cooldown", func(t *testing.T) {
		u, m := newAuth(t)
		allowAll(m)
		m.secrets.EXPECT().OTPCode().Return("123456", nil)
		m.otp.EXPECT().Start(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).Return(false, nil)
		_, err := u.RequestOTP(ctx, req, client)
		assert.Equal(t, inbound.CodeOTPResendTooSoon, code(err))
		assert.Equal(t, "60", apperror.From(err).Details["retryAfterSeconds"])
	})
	t.Run("mail fails clears challenge", func(t *testing.T) {
		u, m := newAuth(t)
		allowAll(m)
		m.secrets.EXPECT().OTPCode().Return("123456", nil)
		m.otp.EXPECT().Start(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).Return(true, nil)
		m.mailer.EXPECT().SendOTP(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).Return(errors.New("smtp down"))
		m.otp.EXPECT().Clear(gomock.Any(), emailKey).Return(errors.New("also down"))
		_, err := u.RequestOTP(ctx, req, client)
		assert.Equal(t, apperror.CodeDependencyUnavailable, code(err))
	})
}

func expectSession(m mocks, rotateFrom string) {
	m.secrets.EXPECT().RefreshToken().Return("refresh-raw", nil)
	sess := entity.Session{ID: "s-2", AccountID: "acc-1", ExpiresAt: now.Add(settings.RefreshTTL)}
	if rotateFrom == "" {
		m.sessions.EXPECT().Create(gomock.Any(), entity.NewSession{AccountID: "acc-1", RefreshTokenHash: []byte("h:refresh-raw"),
			UserAgent: "test", ExpiresAt: now.Add(settings.RefreshTTL)}).Return(sess, nil)
	} else {
		m.sessions.EXPECT().Rotate(gomock.Any(), rotateFrom, gomock.Any(), now).Return(sess, nil)
	}
	m.tokens.EXPECT().IssueAccess("acc-1", now).Return("access-jwt", now.Add(15*time.Minute), nil)
}

func TestVerifyOTP_ExistingAccount(t *testing.T) {
	u, m := newAuth(t)
	m.otp.EXPECT().Attempt(gomock.Any(), emailKey).Return(entity.OTPChallenge{CodeHash: codeH, Attempts: 1}, true, nil)
	m.otp.EXPECT().Clear(gomock.Any(), emailKey).Return(nil)
	m.accounts.EXPECT().FindByEmailHash(gomock.Any(), emailH).Return(activeAcc, true, nil)
	m.accounts.EXPECT().TouchLogin(gomock.Any(), "acc-1", now).Return(nil)
	expectSession(m, "")

	resp, err := u.VerifyOTP(context.Background(), dto.VerifyOTPRequest{Email: "arnu@gmail.com", Code: "123456"}, client)
	require.NoError(t, err)
	assert.Equal(t, "access-jwt", resp.AccessToken)
	assert.Equal(t, "refresh-raw", resp.RefreshToken)
	assert.Equal(t, "acc-1", resp.Account.AccountID)
}

func TestVerifyOTP_FirstLoginCreatesAccount(t *testing.T) {
	u, m := newAuth(t)
	m.otp.EXPECT().Attempt(gomock.Any(), emailKey).Return(entity.OTPChallenge{CodeHash: codeH, Attempts: 1}, true, nil)
	m.otp.EXPECT().Clear(gomock.Any(), emailKey).Return(nil)
	m.accounts.EXPECT().FindByEmailHash(gomock.Any(), emailH).Return(entity.Account{}, false, nil)
	m.pii.EXPECT().SealEmail("arnu@gmail.com").Return([]byte("enc"), nil)
	m.accounts.EXPECT().Create(gomock.Any(), entity.NewAccount{EmailHash: emailH, EmailEnc: []byte("enc"), EmailMasked: "ar***@gmail.com"}).Return(activeAcc, nil)
	m.accounts.EXPECT().TouchLogin(gomock.Any(), "acc-1", now).Return(nil)
	expectSession(m, "")

	_, err := u.VerifyOTP(context.Background(), dto.VerifyOTPRequest{Email: "arnu@gmail.com", Code: "123456"}, client)
	require.NoError(t, err)
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
			m.otp.EXPECT().Attempt(gomock.Any(), emailKey).Return(entity.OTPChallenge{}, false, errors.New("down"))
		}, apperror.CodeInternal},
		{"expired", good, func(m mocks) {
			m.otp.EXPECT().Attempt(gomock.Any(), emailKey).Return(entity.OTPChallenge{}, false, nil)
		}, inbound.CodeOTPExpired},
		{"locked after too many attempts", good, func(m mocks) {
			m.otp.EXPECT().Attempt(gomock.Any(), emailKey).Return(entity.OTPChallenge{CodeHash: codeH, Attempts: 6}, true, nil)
			m.otp.EXPECT().Clear(gomock.Any(), emailKey).Return(nil)
		}, inbound.CodeOTPLocked},
		{"wrong code", dto.VerifyOTPRequest{Email: "arnu@gmail.com", Code: "654321"}, func(m mocks) {
			m.otp.EXPECT().Attempt(gomock.Any(), emailKey).Return(entity.OTPChallenge{CodeHash: codeH, Attempts: 5}, true, nil)
		}, inbound.CodeOTPInvalid},
		{"clear error", good, func(m mocks) {
			m.otp.EXPECT().Attempt(gomock.Any(), emailKey).Return(entity.OTPChallenge{CodeHash: codeH, Attempts: 1}, true, nil)
			m.otp.EXPECT().Clear(gomock.Any(), emailKey).Return(errors.New("down"))
		}, apperror.CodeInternal},
		{"find error", good, func(m mocks) {
			m.otp.EXPECT().Attempt(gomock.Any(), emailKey).Return(entity.OTPChallenge{CodeHash: codeH, Attempts: 1}, true, nil)
			m.otp.EXPECT().Clear(gomock.Any(), emailKey).Return(nil)
			m.accounts.EXPECT().FindByEmailHash(gomock.Any(), emailH).Return(entity.Account{}, false, errors.New("db"))
		}, apperror.CodeInternal},
		{"seal error", good, func(m mocks) {
			m.otp.EXPECT().Attempt(gomock.Any(), emailKey).Return(entity.OTPChallenge{CodeHash: codeH, Attempts: 1}, true, nil)
			m.otp.EXPECT().Clear(gomock.Any(), emailKey).Return(nil)
			m.accounts.EXPECT().FindByEmailHash(gomock.Any(), emailH).Return(entity.Account{}, false, nil)
			m.pii.EXPECT().SealEmail(gomock.Any()).Return(nil, errors.New("aes"))
		}, apperror.CodeInternal},
		{"create error", good, func(m mocks) {
			m.otp.EXPECT().Attempt(gomock.Any(), emailKey).Return(entity.OTPChallenge{CodeHash: codeH, Attempts: 1}, true, nil)
			m.otp.EXPECT().Clear(gomock.Any(), emailKey).Return(nil)
			m.accounts.EXPECT().FindByEmailHash(gomock.Any(), emailH).Return(entity.Account{}, false, nil)
			m.pii.EXPECT().SealEmail(gomock.Any()).Return([]byte("e"), nil)
			m.accounts.EXPECT().Create(gomock.Any(), gomock.Any()).Return(entity.Account{}, errors.New("db"))
		}, apperror.CodeInternal},
		{"disabled", good, func(m mocks) {
			m.otp.EXPECT().Attempt(gomock.Any(), emailKey).Return(entity.OTPChallenge{CodeHash: codeH, Attempts: 1}, true, nil)
			m.otp.EXPECT().Clear(gomock.Any(), emailKey).Return(nil)
			m.accounts.EXPECT().FindByEmailHash(gomock.Any(), emailH).Return(entity.Account{ID: "acc-1", Status: entity.AccountDisabled}, true, nil)
		}, inbound.CodeAccountDisabled},
		{"touch error", good, func(m mocks) {
			m.otp.EXPECT().Attempt(gomock.Any(), emailKey).Return(entity.OTPChallenge{CodeHash: codeH, Attempts: 1}, true, nil)
			m.otp.EXPECT().Clear(gomock.Any(), emailKey).Return(nil)
			m.accounts.EXPECT().FindByEmailHash(gomock.Any(), emailH).Return(activeAcc, true, nil)
			m.accounts.EXPECT().TouchLogin(gomock.Any(), gomock.Any(), gomock.Any()).Return(errors.New("db"))
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

func TestVerifyOTP_WrongCodeReportsAttemptsLeft(t *testing.T) {
	u, m := newAuth(t)
	m.otp.EXPECT().Attempt(gomock.Any(), emailKey).Return(entity.OTPChallenge{CodeHash: codeH, Attempts: 2}, true, nil)
	_, err := u.VerifyOTP(context.Background(), dto.VerifyOTPRequest{Email: "arnu@gmail.com", Code: "654321"}, client)
	assert.Equal(t, "3", apperror.From(err).Details["attemptsLeft"])
}

func TestOpenSession_Failures(t *testing.T) {
	ctx := context.Background()
	t.Run("refresh generator", func(t *testing.T) {
		u, m := newAuth(t)
		m.secrets.EXPECT().RefreshToken().Return("", errors.New("entropy"))
		_, err := u.openSession(ctx, activeAcc, client, now, "")
		assert.Equal(t, apperror.CodeInternal, code(err))
	})
	t.Run("create", func(t *testing.T) {
		u, m := newAuth(t)
		m.secrets.EXPECT().RefreshToken().Return("r", nil)
		m.sessions.EXPECT().Create(gomock.Any(), gomock.Any()).Return(entity.Session{}, errors.New("db"))
		_, err := u.openSession(ctx, activeAcc, client, now, "")
		assert.Equal(t, apperror.CodeInternal, code(err))
	})
	t.Run("rotate lost race", func(t *testing.T) {
		u, m := newAuth(t)
		m.secrets.EXPECT().RefreshToken().Return("r", nil)
		m.sessions.EXPECT().Rotate(gomock.Any(), "s-1", gomock.Any(), now).Return(entity.Session{}, entity.ErrRevoked)
		_, err := u.openSession(ctx, activeAcc, client, now, "s-1")
		assert.Equal(t, inbound.CodeSessionInvalid, code(err))
	})
	t.Run("issue access", func(t *testing.T) {
		u, m := newAuth(t)
		m.secrets.EXPECT().RefreshToken().Return("r", nil)
		m.sessions.EXPECT().Create(gomock.Any(), gomock.Any()).Return(entity.Session{ID: "s"}, nil)
		m.tokens.EXPECT().IssueAccess(gomock.Any(), gomock.Any()).Return("", time.Time{}, errors.New("sign"))
		_, err := u.openSession(ctx, activeAcc, client, now, "")
		assert.Equal(t, apperror.CodeInternal, code(err))
	})
}

func TestRefresh(t *testing.T) {
	ctx := context.Background()
	req := dto.RefreshRequest{RefreshToken: "old"}
	active := entity.Session{ID: "s-1", AccountID: "acc-1", ExpiresAt: now.Add(time.Hour)}

	t.Run("rotates", func(t *testing.T) {
		u, m := newAuth(t)
		m.sessions.EXPECT().FindByTokenHash(gomock.Any(), []byte("h:old")).Return(active, true, nil)
		m.accounts.EXPECT().GetByID(gomock.Any(), "acc-1").Return(activeAcc, nil)
		expectSession(m, "s-1")
		resp, err := u.Refresh(ctx, req, client)
		require.NoError(t, err)
		assert.Equal(t, "refresh-raw", resp.RefreshToken)
	})
	t.Run("reuse revokes everything", func(t *testing.T) {
		u, m := newAuth(t)
		revoked := active
		revoked.RevokedAt = &now
		m.sessions.EXPECT().FindByTokenHash(gomock.Any(), gomock.Any()).Return(revoked, true, nil)
		m.sessions.EXPECT().RevokeAllForAccount(gomock.Any(), "acc-1", now).Return(nil)
		_, err := u.Refresh(ctx, req, client)
		assert.Equal(t, inbound.CodeSessionInvalid, code(err))
	})

	tests := []struct {
		name  string
		req   dto.RefreshRequest
		setup func(m mocks)
		want  apperror.Code
	}{
		{"empty", dto.RefreshRequest{}, func(mocks) {}, inbound.CodeSessionInvalid},
		{"lookup error", req, func(m mocks) {
			m.sessions.EXPECT().FindByTokenHash(gomock.Any(), gomock.Any()).Return(entity.Session{}, false, errors.New("db"))
		}, apperror.CodeInternal},
		{"unknown", req, func(m mocks) {
			m.sessions.EXPECT().FindByTokenHash(gomock.Any(), gomock.Any()).Return(entity.Session{}, false, nil)
		}, inbound.CodeSessionInvalid},
		{"reuse revoke error", req, func(m mocks) {
			r := active
			r.RevokedAt = &now
			m.sessions.EXPECT().FindByTokenHash(gomock.Any(), gomock.Any()).Return(r, true, nil)
			m.sessions.EXPECT().RevokeAllForAccount(gomock.Any(), gomock.Any(), gomock.Any()).Return(errors.New("db"))
		}, apperror.CodeInternal},
		{"expired", req, func(m mocks) {
			e := active
			e.ExpiresAt = now
			m.sessions.EXPECT().FindByTokenHash(gomock.Any(), gomock.Any()).Return(e, true, nil)
		}, inbound.CodeSessionInvalid},
		{"account gone", req, func(m mocks) {
			m.sessions.EXPECT().FindByTokenHash(gomock.Any(), gomock.Any()).Return(active, true, nil)
			m.accounts.EXPECT().GetByID(gomock.Any(), "acc-1").Return(entity.Account{}, entity.ErrNotFound)
		}, inbound.CodeSessionInvalid},
		{"account error", req, func(m mocks) {
			m.sessions.EXPECT().FindByTokenHash(gomock.Any(), gomock.Any()).Return(active, true, nil)
			m.accounts.EXPECT().GetByID(gomock.Any(), "acc-1").Return(entity.Account{}, errors.New("db"))
		}, apperror.CodeInternal},
		{"disabled", req, func(m mocks) {
			m.sessions.EXPECT().FindByTokenHash(gomock.Any(), gomock.Any()).Return(active, true, nil)
			m.accounts.EXPECT().GetByID(gomock.Any(), "acc-1").Return(entity.Account{ID: "acc-1", Status: entity.AccountDisabled}, nil)
		}, inbound.CodeAccountDisabled},
		{"rotate race", req, func(m mocks) {
			m.sessions.EXPECT().FindByTokenHash(gomock.Any(), gomock.Any()).Return(active, true, nil)
			m.accounts.EXPECT().GetByID(gomock.Any(), "acc-1").Return(activeAcc, nil)
			m.secrets.EXPECT().RefreshToken().Return("r", nil)
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

	t.Run("empty token is no-op", func(t *testing.T) {
		u, _ := newAuth(t)
		assert.NoError(t, u.Logout(ctx, dto.RefreshRequest{}, client))
	})
	t.Run("revokes", func(t *testing.T) {
		u, m := newAuth(t)
		m.sessions.EXPECT().FindByTokenHash(gomock.Any(), []byte("h:tok")).Return(active, true, nil)
		m.sessions.EXPECT().Revoke(gomock.Any(), "s-1", now).Return(nil)
		assert.NoError(t, u.Logout(ctx, req, client))
	})
	t.Run("unknown or revoked is idempotent", func(t *testing.T) {
		u, m := newAuth(t)
		m.sessions.EXPECT().FindByTokenHash(gomock.Any(), gomock.Any()).Return(entity.Session{}, false, nil)
		assert.NoError(t, u.Logout(ctx, req, client))
	})
	t.Run("lookup error", func(t *testing.T) {
		u, m := newAuth(t)
		m.sessions.EXPECT().FindByTokenHash(gomock.Any(), gomock.Any()).Return(entity.Session{}, false, errors.New("db"))
		assert.Equal(t, apperror.CodeInternal, code(u.Logout(ctx, req, client)))
	})
	t.Run("revoke error", func(t *testing.T) {
		u, m := newAuth(t)
		m.sessions.EXPECT().FindByTokenHash(gomock.Any(), gomock.Any()).Return(active, true, nil)
		m.sessions.EXPECT().Revoke(gomock.Any(), gomock.Any(), gomock.Any()).Return(errors.New("db"))
		assert.Equal(t, apperror.CodeInternal, code(u.Logout(ctx, req, client)))
	})
}

func TestAuditFailureDoesNotBreakFlow(t *testing.T) {
	ctrl := gomock.NewController(t)
	audit := mock_outbound.NewMockILoginAudit(ctrl)
	audit.EXPECT().Record(gomock.Any(), gomock.Any()).Return(errors.New("db"))
	u := NewAuth(AuthDeps{Audit: audit, Logger: slog.New(slog.NewTextHandler(io.Discard, nil))}, settings)
	u.audit(context.Background(), entity.LoginEvent{Outcome: entity.AuditLogout})
	assert.Equal(t, "ab", truncate("abc", 2))
	assert.Equal(t, "abc", truncate("abc", 5))
}
