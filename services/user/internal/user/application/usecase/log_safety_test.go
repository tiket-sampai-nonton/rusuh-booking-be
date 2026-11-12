package usecase

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"go.uber.org/mock/gomock"

	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/user/internal/user/domain/dto"
	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/user/internal/user/domain/entity"
	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/user/internal/user/port/outbound"
)

// P-18 / H9: every new failure branch logs without email, NIK, OTP code, tokens, or their hashes.
func TestLogSafety_NewFailureBranches(t *testing.T) {
	ctx := context.Background()
	reqOTP := dto.RequestOTPRequest{Email: "arnu@gmail.com"}
	refresh := dto.RefreshRequest{RefreshToken: "old"}
	boom := errors.New("backend down")
	active := entity.Session{ID: "s-1", AccountID: "acc-1", ExpiresAt: now.Add(time.Hour)}

	branches := []struct {
		name string
		run  func(t *testing.T, u *Auth, m mocks)
	}{
		{"release fails after ip limiter rejects", func(t *testing.T, u *Auth, m mocks) {
			m.otp.EXPECT().InCooldown(gomock.Any(), emailKey).Return(false, nil)
			m.limiter.EXPECT().Allow(gomock.Any(), "otp:email:"+emailKey, 5, time.Hour).Return(emailTicket, nil)
			m.limiter.EXPECT().Allow(gomock.Any(), "otp:ip:203.0.113.9", 20, time.Hour).Return(denied("rl:ip"), nil)
			m.limiter.EXPECT().Release(gomock.Any(), emailTicket).Return(boom)
			m.audit.EXPECT().Record(gomock.Any(), gomock.Any()).Return(boom)
			_, _ = u.RequestOTP(ctx, reqOTP, client)
		}},
		{"publish fails and cancel fails", func(t *testing.T, u *Auth, m mocks) {
			expectOTPQuota(m)
			m.secrets.EXPECT().OTPCode().Return("123456", nil)
			m.otp.EXPECT().Start(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).Return(true, nil)
			m.pii.EXPECT().SealEmail(gomock.Any()).Return([]byte("e-enc"), nil)
			m.pii.EXPECT().SealOTP(gomock.Any()).Return([]byte("c-enc"), nil)
			m.publisher.EXPECT().PublishOTP(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).Return(boom)
			m.otp.EXPECT().Cancel(gomock.Any(), emailKey).Return(boom)
			_, _ = u.RequestOTP(ctx, reqOTP, client)
		}},
		{"lost SET NX race, refund fails", func(t *testing.T, u *Auth, m mocks) {
			expectOTPQuota(m)
			m.secrets.EXPECT().OTPCode().Return("123456", nil)
			m.otp.EXPECT().Start(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).Return(false, nil)
			m.limiter.EXPECT().Release(gomock.Any(), gomock.Any()).Return(boom).Times(2)
			_, _ = u.RequestOTP(ctx, reqOTP, client)
		}},
		{"session limiter error (refresh)", func(t *testing.T, u *Auth, m mocks) {
			m.limiter.EXPECT().Allow(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).Return(outbound.RateTicket{}, boom)
			m.sessions.EXPECT().FindByTokenHash(gomock.Any(), gomock.Any()).Return(entity.Session{}, false, nil)
			_, _ = u.Refresh(ctx, refresh, client)
		}},
		{"denylist failure on logout", func(t *testing.T, u *Auth, m mocks) {
			u.d.Denylist = &fakeDenylist{err: boom}
			expectSessionLimit(m)
			m.sessions.EXPECT().FindByTokenHash(gomock.Any(), gomock.Any()).Return(active, true, nil)
			m.sessions.EXPECT().Revoke(gomock.Any(), gomock.Any(), gomock.Any()).Return(nil)
			_ = u.Logout(ctx, refresh, client)
		}},
		{"reuse with denylist failure", func(t *testing.T, u *Auth, m mocks) {
			u.d.Denylist = &fakeDenylist{err: boom}
			revoked := active
			at := now.Add(-time.Minute)
			next := "s-2"
			revoked.RevokedAt = &at
			revoked.ReplacedBy = &next
			expectSessionLimit(m)
			m.sessions.EXPECT().FindByTokenHash(gomock.Any(), gomock.Any()).Return(revoked, true, nil)
			m.sessions.EXPECT().RevokeAllForAccount(gomock.Any(), "acc-1", now, gomock.Any()).Return([]string{"s-1"}, nil)
			_, _ = u.Refresh(ctx, refresh, client)
		}},
		{"verify audit failure", func(t *testing.T, u *Auth, m mocks) {
			expectVerifyLimit(m)
			m.otp.EXPECT().Verify(gomock.Any(), emailKey, []byte("other"), 5).Return(entity.OTPAttempt{Verdict: entity.OTPMismatch, Attempts: 1}, nil)
			m.audit.EXPECT().Record(gomock.Any(), gomock.Any()).Return(boom)
			_, _ = u.VerifyOTP(ctx, dto.VerifyOTPRequest{Email: "arnu@gmail.com", Code: "654321"}, client)
		}},
	}
	forbidden := []string{"arnu", "gmail", "123456", "654321", "abcd", "code-hash", "other", "refresh-raw", "h:old", "enc-email", "e-enc", "c-enc", "old"}
	for _, b := range branches {
		t.Run(b.name, func(t *testing.T) {
			var buf bytes.Buffer
			u, m := newAuthWithLogger(t, slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug})))
			b.run(t, u, m)
			for _, f := range forbidden {
				assert.NotContainsf(t, buf.String(), f, "log tidak boleh memuat %q", f)
			}
		})
	}
}
