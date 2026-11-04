//go:build integration

package valkey

import (
	"context"
	"encoding/hex"
	"io"
	"log/slog"
	"strconv"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	"github.com/tiket-sampai-nonton/rusuh-booking-be/pkg/apperror"
	"github.com/tiket-sampai-nonton/rusuh-booking-be/pkg/clock"

	mock_outbound "github.com/tiket-sampai-nonton/rusuh-booking-be/services/user/_mock/user/port/outbound"
	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/user/internal/user/application/usecase"
	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/user/internal/user/domain/dto"
	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/user/internal/user/port/inbound"
)

// P-07 / TC-USR-17: requests that did not start an OTP must not burn the hourly quota (real store + real limiter).
func TestRequestOTP_RejectedRequestsDoNotBurnQuota(t *testing.T) {
	ctx := context.Background()
	c := client(t)
	fixed := time.Date(2026, 10, 19, 5, 0, 0, 0, time.UTC)

	ctrl := gomock.NewController(t)
	pii := mock_outbound.NewMockIPIIProtector(ctrl)
	secrets := mock_outbound.NewMockISecretGenerator(ctrl)
	pub := mock_outbound.NewMockIOTPPublisher(ctrl)
	audit := mock_outbound.NewMockILoginAudit(ctrl)
	emailHash := []byte{0xab, 0xcd, 0x01}
	key := hex.EncodeToString(emailHash)
	pii.EXPECT().HashEmail(gomock.Any()).Return(emailHash).AnyTimes()
	pii.EXPECT().HashOTP(gomock.Any(), gomock.Any()).Return([]byte("h")).AnyTimes()
	pii.EXPECT().SealEmail(gomock.Any()).Return([]byte("e"), nil).AnyTimes()
	pii.EXPECT().SealOTP(gomock.Any()).Return([]byte("c"), nil).AnyTimes()
	secrets.EXPECT().OTPCode().Return("123456", nil).AnyTimes()
	pub.EXPECT().PublishOTP(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).Return(nil).AnyTimes()
	audit.EXPECT().Record(gomock.Any(), gomock.Any()).Return(nil).AnyTimes()

	store := NewOTPStore(c.VK(), c)
	limiter := NewRateLimiter(c.VK(), c, func() time.Time { return fixed })
	u := usecase.NewAuth(usecase.AuthDeps{OTP: store, Limiter: limiter, PII: pii, Secrets: secrets, Publisher: pub, Audit: audit,
		Clock: clock.Fixed{T: fixed}, Logger: slog.New(slog.NewTextHandler(io.Discard, nil))},
		usecase.AuthSettings{OTPTTL: 5 * time.Minute, ResendCooldown: time.Minute, MaxAttempts: 5, MaxPerEmailHour: 5, MaxPerIPHour: 20})
	cl := inbound.ClientInfo{IP: "203.0.113.9"}
	req := dto.RequestOTPRequest{Email: "arnu@gmail.com"}

	emailKey := c.Key("rl:{otp:email:" + key + "}:" + strconv.FormatInt(fixed.UnixMilli()/time.Hour.Milliseconds(), 10))

	_, err := u.RequestOTP(ctx, req, cl)
	require.NoError(t, err)
	for i := 0; i < 4; i++ {
		_, err = u.RequestOTP(ctx, req, cl)
		require.Error(t, err)
		assert.Equal(t, inbound.CodeOTPResendTooSoon, apperror.From(err).Code)
	}
	assert.EqualValues(t, 1, counter(t, c, emailKey), "1 diterima + 4 ditolak cooldown = 1 unit kuota")

	// empat permintaan sungguhan lagi (cooldown dianggap sudah lewat) tetap lolos; yang keenam kena batas
	for i := 0; i < 4; i++ {
		require.NoError(t, store.Cancel(ctx, key))
		_, err = u.RequestOTP(ctx, req, cl)
		require.NoError(t, err, "permintaan sungguhan ke-%d", i+2)
	}
	require.NoError(t, store.Cancel(ctx, key))
	_, err = u.RequestOTP(ctx, req, cl)
	require.Error(t, err)
	assert.Equal(t, apperror.CodeRateLimited, apperror.From(err).Code)
}
